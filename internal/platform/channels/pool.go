// Package channels 是多协议渠道池：存储（channels.json v2）、Ability 索引与选路的数据层。
//
// 做什么：Channel 全量字段持久化（原子写）+ 多凭证状态（轮询/禁用）+ 旧格式自动迁移
// （Ability 索引见 ability.go，选路见 selector.go）。索引不是第二份配置：
// 进程启动与每次变更后按渠道全量重建，RebuildAbility 是显式修复入口。
// 凭证纪律：Credential 是不透明字符串（单 Key / 换行分隔多 Key / OAuth JSON / AK|SK 复合），
// 本层只做"按行拆分与下标选择"，绝不解释内容；解释权在适配器（internal/platform/adaptors）。
package channels

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// fileFormat 是 channels.json 的结构（当前 v3）。Version=0 视为旧格式并自动迁移；
// ApprovalTools（审批策略）是用户设置，随渠道文件同源持久化。
//
// ApprovalTools **不带 omitempty**（0.0.05）：空列表必须落盘为 "approvalTools": []。
// 为什么：此前 omitempty 让"用户在界面里显式清空审批"与"旧版本文件根本没有这个
// 字段"在磁盘上同形（都缺字段），导致缺字段的旧文件升级后永远无法安全地补上
// 审批默认值——补了就会覆盖"明确选择关闭"的用户。显式关闭从此有独立形态。
type fileFormat struct {
	Version       int       `json:"version"`
	Channels      []Channel `json:"channels"`
	ActiveID      string    `json:"activeId"`
	ApprovalTools []string  `json:"approvalTools"`
	// Proxy 是全局上游代理（0.2.22）：http(s)://host:port；空 = 直连。
	// 为什么全局而非仅渠道级：OAuth 授权发生在"还没有渠道"的时刻，
	// 且地区封锁是整条出口链路的问题（授权与推理会被同一地区策略拒绝）。
	Proxy string `json:"proxy,omitempty"`
}

// DefaultPath 返回渠道配置路径（沿用旧路径，升级即原地迁移）。
func DefaultPath() string { return filepath.Join(configfile.Dir(), "channels.json") }

// fileVersion 是 channels.json 的当前结构版本。v3（0.0.28 驾驶舱）：审批清单
// 一次性补入 mcp/browser 两个新口子（见 Load 内迁移说明）。
const fileVersion = 3

// defaultApprovalTools 是新装默认的审批清单：四个"不确认就执行即危险"的口子——
// shell（任意命令）、ext_manage（扩展增删，能把持久化提示写进后续每轮）、
// mcp（可调宿主挂载的任意工具）、browser（可提交表单）（0.2.36 审计 R3 起
// 前两项，0.0.28 驾驶舱补后两项）。默认零干扰只应是"用户显式关掉"的选择。
var defaultApprovalTools = func() []string {
	return []string{"shell", "ext_manage", "mcp", "browser"}
}

// withApprovalTools 把新增工具追加进既有审批清单：保留用户已有项与顺序、去重。
func withApprovalTools(list []string, add ...string) []string {
	seen := make(map[string]bool, len(list)+len(add))
	out := make([]string, 0, len(list)+len(add))
	for _, t := range list {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range add {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// NewID 生成渠道标识（时间戳前缀，便于排障时判断创建顺序）。
func NewID() string { return fmt.Sprintf("ch-%d", time.Now().UnixNano()) }

// Pool 是渠道池：存储 + Ability 索引 + 凭证状态。同进程并发安全。
type Pool struct {
	path string
	// rand 返回 [0,n) 随机数；加权随机使用（测试可注入确定性源）
	rand func(n int) int

	mu            sync.Mutex
	channels      []*Channel
	activeID      string
	approvalTools []string
	proxy         string                      // 全局上游代理（空 = 直连）
	byGroupModel  map[string]map[string][]int // group → model → channels 下标（priority 降序）
}

// NewPool 构造渠道池（内存态，用 Load 载入文件）。
func NewPool(path string) *Pool {
	return &Pool{path: path, rand: defaultRand}
}

// defaultRand 默认随机源（加权随机仅影响负载分布，不追求密码学强度）。
var defaultRand = func(n int) int { return rand.Intn(n) }

// Load 读取渠道文件并重建 Ability 索引。文件不存在返回空池（合法状态）。
// 旧格式（llm.Channel 单渠道形态，无 version 字段）自动迁移为 v2。
func (p *Pool) Load() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			// 新装（文件尚不存在）：注入默认审批清单并落盘（0.2.36 审计 R3）。
			// shell 与 ext_manage 是"默认无人确认即执行"的两个危险口子：前者等于
			// 任意命令，后者能把持久化提示写进后续每轮的系统说明。默认零干扰
			// 只应是"用户显式关掉"的选择，不是出厂状态。
			p.approvalTools = defaultApprovalTools()
			p.rebuildAbilityLocked()
			return p.persistLocked()
		}
		return fmt.Errorf("渠道配置读取失败 %s：%w", p.path, err)
	}
	// approvalTools 字段存在性探测（0.0.05）：不靠版本号、不解析字段值——
	// 只看原始 JSON 里有没有这个键。此前 omitempty 让"旧版本没这字段"与
	// "用户显式清空"在磁盘同形，升级用户的审批闸门静默失效且无法安全补上。
	var rawKeys map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawKeys); err != nil {
		return fmt.Errorf("渠道配置解析失败 %s：%w", p.path, err)
	}
	_, approvalSet := rawKeys["approvalTools"]

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return fmt.Errorf("渠道配置解析失败 %s：%w", p.path, err)
	}
	if ff.Version == 0 {
		ff.Channels, ff.ActiveID = migrateLegacy(data)
	}
	p.setLocked(ff)
	p.rebuildAbilityLocked()
	if !approvalSet {
		// 缺字段 = 旧版本文件（v0 旧格式同样缺）：一次性迁移为默认审批清单并
		// 落盘——此后磁盘有该字段，"显式关闭"（本版起落盘为 []）永远受尊重。
		// 代价与取舍：曾在旧版本手动关掉审批的用户会被再问一次；这个群体远
		// 小于"所有老用户的 shell/ext_manage 继续无人确认直接执行"。
		p.approvalTools = defaultApprovalTools()
		p.rebuildAbilityLocked()
		return p.persistLocked()
	}
	// v2 → v3 一次性迁移（0.0.28 驾驶舱）：mcp（可调宿主任意工具）与 browser
	// （可提交表单）是新增的"不确认就执行即危险"口子，追加进既有**非空**清单
	// （保留用户已有项、去重）。显式空清单不受影响：那是用户关掉整个审批的
	// 独立形态（0.0.05 起），悄悄重新打开等于推翻其明确选择。升版本号保证迁移
	// 只做一次——此后用户把 mcp/browser 移出清单，重启也不会被补回。
	if ff.Version < fileVersion {
		if len(p.approvalTools) > 0 {
			p.approvalTools = withApprovalTools(p.approvalTools, "mcp", "browser")
		}
		return p.persistLocked()
	}
	return nil
}

// migrateLegacy 把旧格式（protocol/baseUrl/apiKey/model 单渠道形态）映射为 v2：
// type=protocol、models=[model]、groups=[default]、priority=100、credential=apiKey。
func migrateLegacy(data []byte) ([]Channel, string) {
	var legacy struct {
		Channels []llm.Channel `json:"channels"`
		ActiveID string        `json:"activeId"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, "" // 既非 v2 也非旧格式：视为空配置（由调用方引导）
	}
	out := make([]Channel, 0, len(legacy.Channels))
	for _, c := range legacy.Channels {
		out = append(out, Channel{
			ID:         c.ID,
			Type:       string(c.Protocol),
			Name:       c.Name,
			BaseURL:    c.BaseURL,
			Credential: c.APIKey,
			Models:     []string{c.Model},
			Groups:     []string{DefaultGroup},
			Status:     StatusEnabled,
			Priority:   100,
		})
	}
	return out, legacy.ActiveID
}

func (p *Pool) setLocked(ff fileFormat) {
	p.channels = make([]*Channel, 0, len(ff.Channels))
	for i := range ff.Channels {
		p.channels = append(p.channels, &ff.Channels[i])
	}
	p.activeID = ff.ActiveID
	p.approvalTools = ff.ApprovalTools
	p.proxy = ff.Proxy
}

// persistLocked 原子写入当前状态（C-CH-5：temp + fsync + rename，崩溃不留半文件）。
func (p *Pool) persistLocked() error {
	ff := fileFormat{Version: fileVersion, ActiveID: p.activeID, ApprovalTools: p.approvalTools, Proxy: p.proxy}
	ff.Channels = make([]Channel, 0, len(p.channels))
	for _, c := range p.channels {
		ff.Channels = append(ff.Channels, *c)
	}
	data, err := json.MarshalIndent(ff, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileAtomic(p.path, append(data, '\n'), 0o600)
}
