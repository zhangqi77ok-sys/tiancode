// Package tones 保存"回答语气"的用户设置（tones.json，与 extensions.json 同目录）。
//
// 为什么单独一个文件、单独一份常量表：语气是**回答方式**的开关——不是喂料（技能是
// 喂料，正文随场景变），也不是连接（渠道是连接）。内置 50 条是代码常量：不能改、
// 不能删、不能新增；文件只存三件事：固定还是自动、默认哪一条、停用了哪几条。
//
// 为什么拼出来的文本必须逐字稳定：这段文本进系统提示，技能清单 / 环境事实 / 语气
// 设置都不变时拼接结果必须逐字相同——上游 prompt cache 与 applyDynamicPreface 的
// "文本未变不替换"都建立在这个性质上。自动模式因此**不在每一轮重写提示**：名单是
// 固定的，选哪一条由模型按最后一条用户消息自己决定。
package tones

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// 模式只允许这两个值。
const (
	ModeFixed = "fixed" // 固定：只按默认那一条回答
	ModeAuto  = "auto"  // 自动：模型按最后一条用户消息在这份名单里选一条
)

// DefaultID 是缺省语气：文件缺失、非法值都回落到它。
const DefaultID = "plain"

// FactConstraint 是两种模式都带的那一句：语气管"怎么说"，这一句管"依据什么说"。
const FactConstraint = "文件内容、命令输出、接口、行号只引用工具结果或用户原文；没有就说没有，不要编。"

// Entry 是一条内置语气：id、名称、一句做法（做法是行为要求，逐字固定）。
type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Practice string `json:"practice"`
}

// File 是 tones.json 的内容（字段与文件一一对应）。
type File struct {
	Mode     string   `json:"mode"`     // fixed / auto
	Default  string   `json:"default"`  // 内置 id，且不能出现在 Disabled 里
	Disabled []string `json:"disabled"` // 被停用的内置 id
}

// Defaults 是缺文件时的设置：{ "mode": "fixed", "default": "plain", "disabled": [] }。
func Defaults() File {
	return File{Mode: ModeFixed, Default: DefaultID, Disabled: []string{}}
}

// Store 读写语气设置。
type Store struct {
	path string
	mu   sync.Mutex
}

// New 使用给定路径；空路径则用 %APPDATA%\tiancode\tones.json。
func New(path string) *Store {
	if path == "" {
		path = filepath.Join(configfile.Dir(), "tones.json")
	}
	return &Store{path: path}
}

// Load 读取设置：文件不存在 = 默认值（不是错误，首次运行就是这种状态）；
// 内容里的非法值按 Normalize 回落。解析失败返回错误——坏文件必须可见，
// 不能静默当成"没有语气"（用户会以为自己是照设置回答的）。
func (s *Store) Load() (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Save 写盘：先校验（mode 合法、default 是内置且未被停用、disabled 都是内置 id），
// 校验不过直接报错**不落盘**——半合法的设置写进去，之后读回来的行为会跟界面不一致。
func (s *Store) Save(f File) error {
	if err := Validate(f); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(f)
}

// Validate 校验设置合法性（保存路径的入口规则）。
func Validate(f File) error {
	if f.Mode != ModeFixed && f.Mode != ModeAuto {
		return fmt.Errorf("未知的语气模式：%q（只允许 %s / %s）", f.Mode, ModeFixed, ModeAuto)
	}
	if _, ok := lookup(f.Default); !ok {
		return fmt.Errorf("未知的默认语气：%q（只能从内置语气里选）", f.Default)
	}
	for _, id := range f.Disabled {
		if _, ok := lookup(id); !ok {
			return fmt.Errorf("未知的语气 id：%q（只能停用内置语气）", id)
		}
		if id == f.Default {
			return fmt.Errorf("默认语气 %q 不能停用：请先把默认换成别的语气", id)
		}
	}
	return nil
}

// Normalize 把非法值回落到 fixed + plain：手改文件、旧版本残留都可能带非法值，
// 读出来必须是能直接用的设置。default 无效或被停用时退到 plain；plain 也被停用
// （手改出来的状态）则取第一条未停用的；一条不剩时留空串，注入时按 plain 回落。
func Normalize(f File) File {
	out := f
	if out.Disabled == nil {
		out.Disabled = []string{}
	}
	if out.Mode != ModeFixed && out.Mode != ModeAuto {
		out.Mode = ModeFixed
	}
	if _, ok := lookup(out.Default); !ok || contains(out.Disabled, out.Default) {
		out.Default = fallbackDefault(out.Disabled)
	}
	return out
}

// Enabled 返回未停用的条目（保持内置顺序——拼接结果因此逐字稳定）。
func Enabled(f File) []Entry {
	out := make([]Entry, 0, len(Builtin))
	for _, e := range Builtin {
		if !contains(f.Disabled, e.ID) {
			out = append(out, e)
		}
	}
	return out
}

// Section 是注入系统提示的语气段：设置不变时逐字相同（调用方负责拼接顺序）。
func Section(f File) string {
	f = Normalize(f)
	def, ok := lookup(f.Default)
	if !ok {
		def, _ = lookup(DefaultID) // 内置语气全被停用（手改文件）：按回落规则用 plain
	}
	var b strings.Builder
	if f.Mode == ModeAuto {
		b.WriteString("## 语气（自动选择）\n")
		b.WriteString(fmt.Sprintf("- 默认语气：%s\n", def.ID))
		b.WriteString("- 候选语气（每行一条：id 名称：做法）：\n")
		for _, e := range Enabled(f) {
			b.WriteString(fmt.Sprintf("  - %s %s：%s\n", e.ID, e.Name, e.Practice))
		}
		b.WriteString("- 只根据最后一条用户消息在这份名单里选一条，整段回答只按那一条；" +
			"用户本条明确指定了语气就听用户的；没有合适的就用默认语气；不要混用，不要发明名单外的语气。\n")
	} else {
		b.WriteString("## 语气（固定）\n")
		b.WriteString(fmt.Sprintf("- %s\n", def.Practice))
	}
	b.WriteString("- " + FactConstraint + "\n")
	return b.String()
}

// load 是持锁约定下的读实现（Load / 内部调用共用）。
func (s *Store) load() (File, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("语气设置解析失败 %s：%w", s.path, err)
	}
	return Normalize(f), nil
}

// write 是持锁约定下的写实现：原子写（半截 JSON 会让整份设置读不出来）。
func (s *Store) write(f File) error {
	if f.Disabled == nil {
		f.Disabled = []string{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化语气设置失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("准备语气设置目录失败：%w", err)
	}
	if err := atomicfile.WriteFileAtomic(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("保存语气设置失败：%w", err)
	}
	return nil
}

// lookup 按 id 取内置条目。
func lookup(id string) (Entry, bool) {
	for _, e := range Builtin {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// fallbackDefault 在 plain 可用时用 plain，否则取第一条未停用的；一条都没有则空串。
func fallbackDefault(disabled []string) string {
	if !contains(disabled, DefaultID) {
		return DefaultID
	}
	for _, e := range Builtin {
		if !contains(disabled, e.ID) {
			return e.ID
		}
	}
	return ""
}

// contains 判断 id 是否在列表里（列表最多 50 项，线性查足够且无分配）。
func contains(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
