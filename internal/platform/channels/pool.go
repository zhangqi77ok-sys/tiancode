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

// fileFormat 是 channels.json v2 的结构。Version=0 视为旧格式并自动迁移；
// ApprovalTools（审批策略）是用户设置，随渠道文件同源持久化。
type fileFormat struct {
	Version       int       `json:"version"`
	Channels      []Channel `json:"channels"`
	ActiveID      string    `json:"activeId"`
	ApprovalTools []string  `json:"approvalTools,omitempty"`
}

// DefaultPath 返回渠道配置路径（沿用旧路径，升级即原地迁移）。
func DefaultPath() string { return filepath.Join(configfile.Dir(), "channels.json") }

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
			p.rebuildAbilityLocked()
			return nil
		}
		return fmt.Errorf("渠道配置读取失败 %s：%w", p.path, err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return fmt.Errorf("渠道配置解析失败 %s：%w", p.path, err)
	}
	if ff.Version == 0 {
		ff.Channels, ff.ActiveID = migrateLegacy(data)
	}
	p.setLocked(ff)
	p.rebuildAbilityLocked()
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
}

// persistLocked 原子写入当前状态（C-CH-5：temp + fsync + rename，崩溃不留半文件）。
func (p *Pool) persistLocked() error {
	ff := fileFormat{Version: 2, ActiveID: p.activeID, ApprovalTools: p.approvalTools}
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
