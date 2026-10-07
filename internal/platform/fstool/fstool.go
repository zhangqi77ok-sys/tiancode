// Package fstool 实现工作区受控文件工具（read/write/replace/list）。
// 做什么：把模型的结构化文件操作转换为受路径校验与原子写保护的磁盘操作。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、core/llm（定义形态）、platform/atomicfile、stdlib。
//
// 执行契约（C-FS-1~7、C-TOOL-1）：路径越界拒绝；replace 多处/零匹配报错且文件
// 零修改；write 走原子写；list 非递归且有界；内部施加超时（30s——fs 操作快，宽裕即安全）。
package fstool

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/codeintel"
	"tiancode/internal/platform/workspace"
)

// fsTimeout 是单个文件操作的超时上限。
// 为什么 30s：本地磁盘操作毫秒级完成，30s 只防御文件系统病态（网络盘/杀软锁死）。
const fsTimeout = 30 * time.Second

// Tool 是工作区受控文件工具。
type Tool struct {
	root string // 工作区绝对路径，所有路径必须落在其内

	// lastRead 记录每个路径**最近一次成功 read 是否覆盖全文**（0.0.07 write 门卫）：
	// 已存在的文件只有整读过才允许 write 整体替换——模型只读片段再 write 会把
	// 未读的后半段静默丢掉。记录只活在当前进程、当前会话的实例里（会话级 fs
	// 工具各自一份，0.0.36 R1），重启即失效，失效就拒绝覆盖——宁可让模型多读一次。
	// 绝不写进任何送给模型的文件。
	mu       sync.Mutex
	lastRead map[string]bool

	// 轮次检查点（第 6 批）：本轮首次修改某文件前的快照（roundCP/roundOrder 保序），
	// roundOn 只在 BeginRound~EndRound 之间为真。由编排层在每次 Send 前后驱动。
	roundCP    map[string]*tools.RoundCheckpoint
	roundOrder []string
	roundOn    bool
}

// BeginRound 开始新一轮检查点收集（编排层每次 Send 前调用；清空上一轮残留）。
func (t *Tool) BeginRound() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roundCP = map[string]*tools.RoundCheckpoint{}
	t.roundOrder = nil
	t.roundOn = true
}

// EndRound 结束本轮收集并按首次修改顺序返回检查点（Map 无序，靠 roundOrder 保序）。
func (t *Tool) EndRound() []tools.RoundCheckpoint {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roundOn = false
	out := make([]tools.RoundCheckpoint, 0, len(t.roundOrder))
	for _, p := range t.roundOrder {
		if cp, ok := t.roundCP[p]; ok {
			out = append(out, *cp)
		}
	}
	t.roundCP = map[string]*tools.RoundCheckpoint{}
	t.roundOrder = nil
	return out
}

// noteRound 记录本轮首次修改该文件前的快照；已记录的文件只更新"最后写入哈希"
// （撤回前校验用），绝不覆盖首次快照——那才是"本轮开始前"的状态。
func (t *Tool) noteRound(path, old string, existed, haveOld bool, newSHA string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.roundOn {
		return
	}
	if cp, ok := t.roundCP[path]; ok {
		cp.LastSHA256 = newSHA
		return
	}
	cp := &tools.RoundCheckpoint{Path: path, OldExists: existed, LastSHA256: newSHA}
	if existed && !haveOld {
		cp.Note = "无法撤回：写入前的内容超过上限，未保存快照"
	} else {
		cp.OldContent = old
	}
	t.roundCP[path] = cp
	t.roundOrder = append(t.roundOrder, path)
}

// RestoreCheckpoint 按轮次检查点恢复一个文件（「撤回本轮」）。
// 校验纪律与单文件「恢复写入前」一致：当前内容必须与本轮最后一次写入一致
// （哈希比对）——用户在本轮之后手工改过就拒绝，绝不覆盖用户改动。
func (t *Tool) RestoreCheckpoint(cp tools.RoundCheckpoint) error {
	if cp.Note != "" {
		return errors.New(cp.Note)
	}
	full, err := t.resolve(cp.Path)
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(full)
	switch {
	case err == nil:
		if cp.LastSHA256 != "" && sha256Hex(cur) != cp.LastSHA256 {
			return errors.New("文件在本轮之后被改过，已跳过（不覆盖你的改动）")
		}
	case os.IsNotExist(err):
		if cp.OldExists {
			return errors.New("文件已被删除，已跳过")
		}
		return nil // 本轮新建且现在不存在：无需恢复
	default:
		return err
	}
	if !cp.OldExists {
		// 本轮新建的文件：撤回 = 删除（与单文件撤销的 OldExists=false 语义一致）
		return os.Remove(full)
	}
	return atomicfile.WriteFileAtomic(full, []byte(cp.OldContent), 0o600)
}

// UndoSnapshot 与 tools.UndoData 同一类型（结果字段直接透传）。
type UndoSnapshot = tools.UndoData

// New 构造工具，root 为工作区绝对路径。
//
// root 与候选路径必须同一套真实路径解析：resolve 对候选做 EvalSymlinks
// （符号链接审计的另一半），Windows 上这一步会把 8.3 短名（如 CI 的
// RUNNER~1）解成长名——若 root 保持短名原样，前缀比对会把整个工作区
// 误判成越界（CI 实测：全部 write 报 "path escapes workspace: a.txt"）。
// 解析失败（root 尚不存在等）保持原值，行为与旧版一致。
func New(root string) *Tool {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return &Tool{root: root, lastRead: map[string]bool{}}
}

// Root 返回工作区根路径（测试与审计用）。
func (t *Tool) Root() string { return t.root }

// Name 实现工具端口。
func (t *Tool) Name() string { return "fs" }

// Description 实现工具端口。
// 行号纪律（0.0.07）：read 输出带 "行号|正文" 前缀——replace 的 target 必须是
// 文件**原文**，绝不能把行号前缀复制进去（否则永远零匹配）。
func (t *Tool) Description() string {
	return "读写工作区文件（read/write）、精准局部替换（replace，多处匹配默认拒绝；同一文件多处修改推荐 edits 多段形态——一次调用原子应用，省往返）、非递归目录列表（list，最多 500 条）与目录骨架（tree，深度 2、最多 500 条——先 tree 了解项目结构，再 list 看某个目录的确切内容，不要对大目录用 list 逐层摸）。" +
		"read 输出带 \"行号|正文\" 前缀（如 12|func main() {）：replace 的 target 必须是不含行号前缀的文件原文。write 只能覆盖本会话整读过的文件——没读过或只读过片段的已有文件会被拒绝，请先整读或改用 replace。" +
		"写完 .go 文件系统会自动做编译级诊断（go vet，含 _test.go）并把错误带回；这份诊断只覆盖该文件所在包，跨包影响不会出现，需要时自行跑 go test。action=diagnose 手动触发；action=symbols 看文件符号大纲（函数/方法/类型带行号，先读大纲再精读，别盲猜行号）。"
}

// Schema 实现工具端口：参数 JSON Schema。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["read", "write", "replace", "list", "tree", "diagnose", "symbols"]},
    "path": {"type": "string", "description": "相对工作区的路径"},
    "content": {"type": "string", "description": "write 时的完整文件内容"},
    "target": {"type": "string", "description": "replace 时的精确目标文本（文件原文，不含 read 输出的行号前缀）"},
    "replacement": {"type": "string", "description": "replace 时的替换文本"},
    "edits": {
      "type": "array",
      "description": "多段编辑（推荐用于同一文件的多处修改）：逐段 {target, replacement}，一次调用原子应用——任一段匹配不上则整次失败、文件零修改；后面的段在前面的段应用后的内容上匹配。与 target/replacement 二选一",
      "items": {
        "type": "object",
        "properties": {
          "target": {"type": "string"},
          "replacement": {"type": "string"}
        },
        "required": ["target", "replacement"]
      }
    },
    "allow_multiple": {"type": "boolean", "description": "replace 多处匹配时是否全部替换（默认 false）"},
    "start_line": {"type": "integer", "description": "read 时的起始行（1 起；缺省 1）。大文件分段读取用行号，绝不按字节切（多字节字符安全）"},
    "line_count": {"type": "integer", "description": "read 时的读取行数（缺省到文件尾）"}
  },
  "required": ["action", "path"]
}`)
}

// Execute 实现工具端口（遵守执行契约：内部超时/业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, fsTimeout)
	defer cancel()

	var args struct {
		Action        string   `json:"action"`
		Path          string   `json:"path"`
		Content       string   `json:"content"`
		Target        string   `json:"target"`
		Replacement   string   `json:"replacement"`
		AllowMultiple bool     `json:"allow_multiple"`
		StartLine     int64    `json:"start_line"`
		LineCount     int64    `json:"line_count"`
		Edits         []fsEdit `json:"edits"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return bizErrf("invalid arguments: %v", err), nil
	}

	// 卡片语义标签：defer 覆盖全部返回路径（成功/业务失败/取消），失败卡也能显示"动了哪个文件"
	defer func() {
		if res.Title == "" {
			res.Title, res.Op = fsTitle(args.Path), fsOp(args.Action)
		}
	}()

	// 取消响应：入口即检查（C-TOOL-5 协作式取消）
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}

	// 写侧参数上限（0.2.36 审计 R6）：上限高于读取（可编辑锁文件/生成产物），
	// 超限**整次失败**——不截断、不落盘（原子写保证失败不留下半个文件）。
	// 已知局限：这是文件工具层的第一道闸（JSON 反序列化前内存已分配）；
	// 真正挡住超大工具参数还需要 agent 参数层的长度限制（另一层，另行处理）。
	if len(args.Content) > maxWriteBytes {
		return bizErrf("content too large (%d bytes > %d)：请拆分为多次写入", len(args.Content), maxWriteBytes), nil
	}
	if len(args.Replacement) > maxWriteBytes || len(args.Target) > maxWriteBytes {
		return bizErrf("target/replacement too large (> %d)：请拆分替换", maxWriteBytes), nil
	}

	switch args.Action {
	case "read":
		return t.read(args.Path, args.StartLine, args.LineCount)
	case "write":
		return t.write(ctx, args.Path, args.Content)
	case "replace":
		return t.replace(ctx, args.Path, args.Target, args.Replacement, args.AllowMultiple, args.Edits)
	case "list":
		return t.list(args.Path)
	case "tree":
		return t.tree(args.Path)
	case "diagnose":
		return t.diagnose(ctx, args.Path)
	case "symbols":
		return t.symbols(args.Path)
	default:
		return bizErrf("unknown action %q (want read/write/replace/list/tree/diagnose/symbols)", args.Action), nil
	}
}

// resolve 校验并解析工作区内路径（C-FS-4：绝对路径与 ../ 逃逸一律拒绝）。
func (t *Tool) resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.Clean(filepath.Join(t.root, path))
	// 符号链接/junction 解析（0.2.35 审计#4）：工作区内的链接可以指到区外，
	// 词法前缀检查拦不住——对已存在的最深前缀解析真实路径后再比对。
	// 目标不存在（写场景）时对父目录解析（父目录在区内，链接才能落到区外）。
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		clean = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
		clean = filepath.Join(real, filepath.Base(clean))
	}
	if !hasRootPrefix(t.root, clean) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

// hasRootPrefix 判断 p 是否在 root 内（0.2.36 审计 R4/R5：两侧同一套规范化；
// Windows 文件系统大小写不敏感——前缀比较必须忽略大小写，否则 C:\Proj 与
// c:\proj 会把整个工作区判成越界）。
func hasRootPrefix(root, p string) bool {
	if p == root {
		return true
	}
	if runtime.GOOS == "windows" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// 读取与写入的硬顶（0.2.36 审计 R3/R6）：
//   - maxReadBytes：单次 read（整读或单个分片）上限；
//   - maxWriteBytes：write/replace 的 content/target/replacement 上限——**高于读取**
//     （锁文件/打包产物等大文本仍可编辑）；超限整次失败、不截断、原文件不动。
const (
	maxReadBytes  = 10 << 20
	maxWriteBytes = 32 << 20
)

// MaxReadBytes 是上面那个单次读取上限的**导出**别名（第 8 批）：文件详情面板的
// 只读浏览复用同一个数字——界面上写的"只显示前 N 字节"必须与工具真实上限一致，
// 不能各写一份。
const MaxReadBytes = maxReadBytes

// read 读取文件（0.0.07 起按**行**分段，字节 offset 已废除——按字节切会截断
// UTF-8 多字节字符，行边界天然安全）。正文带 "行号|正文" 前缀（行号 1 起）。
// 超限语义（0.2.36 审计 R3）保持：整读（未给 start_line/line_count）超过硬顶时
// **拒绝**并给出按行分段的合法出路；分段用流式扫描（大文件不整份进内存）。
// 整读（start_line ≤ 1、未给 line_count、无预算截断、扫到文件尾）会把该路径
// 标记为"本会话整读过"——write 覆盖已存在文件的唯一凭证；片段读置回 false。
func (t *Tool) read(path string, startLine, lineCount int64) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	// 归一化参数：缺省 = 整读；非法值显式拒绝
	if startLine < 0 || lineCount < 0 {
		return bizErrf("start_line/line_count 必须非负"), nil
	}
	if startLine == 0 {
		startLine = 1
	}
	wholeFile := startLine == 1 && lineCount == 0
	if wholeFile && info.Size() > maxReadBytes {
		return bizErrf("file too large (%d bytes > %d)：请用 start_line/line_count 按行分段读取（如 {\"action\":\"read\",\"path\":%q,\"start_line\":1,\"line_count\":2000}）",
			info.Size(), maxReadBytes, path), nil
	}

	// 流式逐行扫描：collect 收集 [startLine, startLine+wanted) 内的行（字节预算
	// 内），total 统计总行数——大文件也不整份进内存，多字节字符按整行返回。
	f, err := os.Open(full)
	if err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLineBytes)

	wanted := int64(-1) // 无限
	if lineCount > 0 {
		wanted = lineCount
	}
	var b strings.Builder
	var collected, total, sum int64
	truncatedByBudget := false
	beyondEOF := true // startLine 落在文件行数之外时置位
	for sc.Scan() {
		total++
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case total < startLine:
			// 未到起始行：仅计数
		case wanted >= 0 && collected >= wanted:
			// 已收满 lineCount：仅计数（用于"共 N 行"）
		default:
			if sum+int64(len(line))+1 > maxReadBytes {
				truncatedByBudget = true // 整行收回：绝不切出半个字符
				continue                 // 继续扫描只为统计总行数
			}
			beyondEOF = false
			fmt.Fprintf(&b, "%d|%s\n", total, line)
			collected++
			sum += int64(len(line)) + 1
		}
	}
	if err := sc.Err(); err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	if startLine > total && total > 0 {
		return bizErrf("start_line %d 超出文件行数 %d", startLine, total), nil
	}
	if beyondEOF && collected == 0 && total > 0 {
		return bizErrf("起始行内容超过读取上限（或单行缓冲 %d 字节）：无法按行读取", maxScanLineBytes), nil
	}

	// 整读判定与标记：从第 1 行、未给行数、无预算截断、扫到文件尾
	fullRead := wholeFile && !truncatedByBudget
	t.mu.Lock()
	t.lastRead[full] = fullRead
	t.mu.Unlock()

	out := strings.TrimRight(b.String(), "\n")
	if !fullRead {
		note := ""
		if truncatedByBudget {
			note = "，按读取上限截断"
		}
		out = fmt.Sprintf("[start_line=%d 读取 %d 行 / 共 %d 行%s]\n", startLine, collected, total, note) + out
	}
	return tools.ToolResult{Content: out}, nil
}

// maxScanLineBytes 是按行读取的单行缓冲上限（超过 = 行太长无法按行处理，
// 显式报错而不是悄悄截断）。1MB 覆盖一切正常源码/文本。
const maxScanLineBytes = 1 << 20

// write 整文件写入。0.0.07 两道新闸：
//   - 整读门卫：目标**已存在**时，仅当本会话最近一次成功 read 覆盖全文才放行——
//     模型只读片段（offset 时代）或分段读了前半就 write，后半段会被静默丢掉。
//     拒绝时写明出路（replace / 先整读）。目标不存在 = 新建，不需要先读。
//   - 撤销快照：写入成功后把旧全文放进 Undo（只给界面/后端恢复用，不进模型
//     上下文）；旧内容超上限时放弃快照并注明"无法恢复"——绝不为恢复多读一份。
func (t *Tool) write(ctx context.Context, path, content string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	// 整读门卫（0.0.07）：已存在的文件必须本会话整读过
	var old string
	haveDiff := false
	existed := false
	var sizeAtPropose int64
	var modAtPropose time.Time
	info, statErr := os.Stat(full)
	switch {
	case statErr == nil:
		t.mu.Lock()
		fullRead := t.lastRead[full]
		t.mu.Unlock()
		if !fullRead {
			return bizErrf("refusing to overwrite %s: 本会话没有整读过这个文件（只读片段就整体覆盖会丢掉未读内容）。已存在的文件请用 replace；若确要整文件重写，先不带 start_line/line_count 整读一遍再 write", path), nil
		}
		existed = true
		sizeAtPropose, modAtPropose = info.Size(), info.ModTime()
		if info.Size() <= maxWriteBytes {
			if data, readErr := os.ReadFile(full); readErr == nil {
				old = string(data)
				haveDiff = true
			} else {
				return bizErrf("read before write failed: %v", readErr), nil
			}
		}
		// 超限：跳过 diff 与撤销快照（不为恢复多读一份超大文件）
	case os.IsNotExist(statErr):
		// 新建：old 为空是真实状态，保留 "+全文" 新建 diff（既有行为）
		haveDiff = true
	default:
		return bizErrf("stat before write failed: %v", statErr), nil
	}

	// applyFn 是真正的落盘：内部自验外部改动（快照到落盘之间文件被其他程序
	// 改过 → 拒绝且原文件不动），原子写，更新整读标记，产出撤销快照。
	applyFn := func() (*UndoSnapshot, error) {
		if existed {
			info2, err := os.Stat(full)
			if err != nil || info2.Size() != sizeAtPropose || !info2.ModTime().Equal(modAtPropose) {
				return nil, errors.New("文件已被其他程序修改，应用失败（原文件未动）")
			}
		}
		if err := atomicfile.WriteFileAtomic(full, []byte(content), 0o600); err != nil {
			return nil, err
		}
		// 写入成功：模型刚给全了内容，视为"已知全文"（后续 write 无需重读）
		t.mu.Lock()
		t.lastRead[full] = true
		t.mu.Unlock()
		u := &UndoSnapshot{Path: path, OldExists: existed, OldContent: old, NewSHA256: sha256Hex([]byte(content))}
		if !existed || !haveDiff {
			u.OldContent = old // 超限时为空串（无法恢复语义由 UndoNote 表达）
		}
		// 第 6 批：轮次检查点（本轮首次修改前快照，供「撤回本轮」）
		t.noteRound(path, old, existed, haveDiff, u.NewSHA256)
		return u, nil
	}

	fullDiff := ""
	if haveDiff {
		fullDiff = diffText(path, old, content)
	}
	undo, err := applyFn()
	if err != nil {
		return bizErrf("write failed: %v", err), nil
	}
	res := tools.ToolResult{
		Content: fmt.Sprintf("written %s (%d bytes)", path, len(content)),
	}
	if haveDiff && undo != nil {
		res.Diff = fullDiff
		res.Content = withShortDiff(res.Content, path, res.Diff)
	}
	switch {
	case existed && haveDiff && undo != nil:
		res.Undo = undo
	case !existed && undo != nil:
		res.Undo = undo // OldExists=false：新建文件没有旧内容
	case existed && !haveDiff:
		// 旧内容超过上限，快照是空的。不挂 Undo（恢复会写成空文件），说明给界面。
		res.UndoNote = "旧内容超过上限，这次无法恢复"
	}
	t.appendDiagnostics(ctx, &res, path)
	return res, nil
}

// sha256Hex 计算内容哈希（恢复前的"被人改过"检测用）。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fsEdit 是多段编辑的一个 hunk（0.0.34）。
type fsEdit struct {
	Target      string `json:"target"`
	Replacement string `json:"replacement"`
}

func (t *Tool) replace(ctx context.Context, path, target, replacement string, allowMultiple bool, edits []fsEdit) (tools.ToolResult, error) {
	if target == "" && len(edits) == 0 {
		return bizErrf("target is required for replace (or pass edits for multi-hunk)"), nil
	}
	// 多段与单段混用在此先拒（主校验在读取文件后，避免空 target 先于越界检查）：
	if len(edits) > 0 && (target != "" || replacement != "") {
		return bizErrf("provide either target/replacement or edits, not both"), nil
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	// 大小硬顶前置（0.2.37 审计）：replace 必须整份读入才能替换——先 Stat，超过
	// 写入硬顶直接拒绝，不打开全文（打开再失败也挡不住那次内存分配）。给出分段
	// 之外的合法出路：用 write 重写小文件，或先 read 分段。
	var info0 os.FileInfo
	if info, statErr := os.Stat(full); statErr != nil {
		if !os.IsNotExist(statErr) {
			return bizErrf("stat before replace failed: %v", statErr), nil
		}
	} else {
		info0 = info
		if info.Size() > maxWriteBytes {
			return bizErrf("file too large to replace (%d bytes > %d)：请用 write 重写小文件，或先用 read 分段确认内容", info.Size(), maxWriteBytes), nil
		}
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return bizErrf("read before replace failed: %v", err), nil
	}
	old := string(data)
	// 形态选择（0.0.34）：edits 非空 = 多段编辑，单段参数必须为空——两套混用只会
	// 制造歧义。多段的原子性是**整次调用**的：任一段失败文件零修改（与单段
	// C-FS-2/3 同语义），绝不留下"改了一半"的文件。
	useEdits := len(edits) > 0
	if useEdits && (target != "" || replacement != "") {
		return bizErrf("provide either target/replacement or edits, not both"), nil
	}
	if !useEdits && target == "" {
		return bizErrf("target is required for replace"), nil
	}
	var updated string
	var count int
	var summary string
	if useEdits {
		updated = old
		total := 0
		for i, e := range edits {
			if strings.TrimSpace(e.Target) == "" {
				return bizErrf("edits[%d].target is required", i), nil
			}
			// 每段在**前序段应用后**的内容上匹配——后面的段可以引用前面段刚改出的文本
			c := strings.Count(updated, e.Target)
			switch {
			case c == 0:
				// 与单段同款：给最相近行的上下文，模型拿真实原文修 target
				msg := fmt.Sprintf("edits[%d] target not found (0 matches): file unchanged", i)
				if near := nearbyLines(updated, e.Target); near != "" {
					msg += "\nnearest match context:\n" + near
				} else {
					msg += "\nno similar line found in file"
				}
				return bizErrf("%s", msg), nil
			case c > 1 && !allowMultiple:
				return bizErrf("edits[%d] target matches %d locations; refusing ambiguous replace (set allow_multiple to replace all)", i, c), nil
			}
			updated = strings.ReplaceAll(updated, e.Target, e.Replacement)
			total += c
		}
		count, summary = total, fmt.Sprintf("applied %d edit(s) (%d occurrence(s)) in %s", len(edits), total, path)
	} else {
		count = strings.Count(old, target)
		if count == 0 {
			// C-FS-3：零匹配报错，文件零修改。0.0.07：不再只说 "not found"——
			// 在文件里找与 target 首行最相近的行，返回该行前后各 2 行（带行号），
			// 模型拿真实上下文修 target；确实没有相近行就明说。
			msg := "target not found (0 matches): file unchanged"
			if near := nearbyLines(old, target); near != "" {
				msg += "\nnearest match context:\n" + near
			} else {
				msg += "\nno similar line found in file"
			}
			return bizErrf("%s", msg), nil
		}
		if count > 1 && !allowMultiple {
			// C-FS-2：多处匹配默认拒绝，防误伤
			return bizErrf("target matches %d locations; refusing ambiguous replace (set allow_multiple to replace all)", count), nil
		}
		updated = strings.ReplaceAll(old, target, replacement)
		summary = fmt.Sprintf("replaced %d occurrence(s) in %s", count, path)
	}
	sizeAtPropose, modAtPropose := info0.Size(), info0.ModTime()
	fullDiff := diffText(path, old, updated)

	// applyFn 是真正的落盘：自验外部改动（快照到落盘间文件被改 → 拒绝）
	applyFn := func() (*UndoSnapshot, error) {
		info2, err := os.Stat(full)
		if err != nil || info2.Size() != sizeAtPropose || !info2.ModTime().Equal(modAtPropose) {
			return nil, errors.New("文件已被其他程序修改，应用失败（原文件未动）")
		}
		if err := atomicfile.WriteFileAtomic(full, []byte(updated), 0o600); err != nil {
			return nil, err
		}
		// 文件已被 replace 改变：此前的"整读过"标记失效（后续 write 需重新整读）
		t.mu.Lock()
		t.lastRead[full] = false
		t.mu.Unlock()
		u := &UndoSnapshot{Path: path, OldExists: true, OldContent: old, NewSHA256: sha256Hex([]byte(updated))}
		// 第 6 批：轮次检查点（本轮首次修改前快照，供「撤回本轮」）
		t.noteRound(path, old, true, true, u.NewSHA256)
		return u, nil
	}

	undo, err := applyFn()
	if err != nil {
		return bizErrf("replace write failed: %v", err), nil
	}
	res := tools.ToolResult{
		Content: withShortDiff(summary, path, fullDiff),
		Diff:    fullDiff,
		Undo:    undo,
	}
	t.appendDiagnostics(ctx, &res, path)
	return res, nil
}

// appendDiagnostics 是 write/replace 成功后的自动编译诊断钩子（C-FS-8）：
// 只对 .go 且工作区是 Go module 时触发；干净时静默，超时/跳过只附一条说明、
// 绝不改写写入的成功语义（诊断是信息，不是闸门）。
func (t *Tool) appendDiagnostics(ctx context.Context, res *tools.ToolResult, path string) {
	r := codeintel.Diagnose(ctx, t.root, filepath.ToSlash(path), codeintel.AutoTimeout)
	// Inconclusive（vet 机制性失败且无可解析诊断，如嵌套 module）自动静默：
	// 每次写入都附"不可判定"是纯噪音；手动 fs.diagnose 会明说。
	if !r.Attempted || r.Inconclusive || ctx.Err() != nil {
		return
	}
	if len(r.Diagnostics) == 0 {
		if r.Note != "" {
			res.Content += "\n[编译诊断] " + r.Note
		}
		return // 干净：静默
	}
	res.Content += "\n" + codeintel.Format(r)
}

// diagnose 手动编译诊断（C-FS-9）：干净/跳过都明说，不静默。
func (t *Tool) diagnose(ctx context.Context, path string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		return bizErrf("not a file: %s", path), nil
	}
	r := codeintel.Diagnose(ctx, t.root, filepath.ToSlash(path), codeintel.ManualCap)
	return tools.ToolResult{Content: codeintel.Format(r)}, nil
}

// symbols 文件符号大纲（C-FS-10）：Go 精确（parser），其余启发式并标注。
func (t *Tool) symbols(path string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("symbols failed: %v", err), nil
	}
	if info.IsDir() {
		return bizErrf("not a file: %s", path), nil
	}
	if info.Size() > maxReadBytes {
		return bizErrf("file too large for symbols (%d bytes > %d)", info.Size(), maxReadBytes), nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return bizErrf("read before symbols failed: %v", err), nil
	}
	ss, note := codeintel.Symbols(path, data)
	if ss == nil {
		return bizErrf("%s", note), nil
	}
	var b strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&b, "L%d %s %s\n", s.Line, s.Kind, s.Text)
	}
	out := strings.TrimRight(b.String(), "\n")
	if note != "" {
		out += "\n" + note
	}
	return tools.ToolResult{Content: out}, nil
}

// splitLines 按行拆分并去掉行尾 \r（CRLF 文件的行处理保持干净）；
// 尾部换行产生的空尾行不算一行（文件以 \n 结尾是常态，不是第 N+1 空行）。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = strings.TrimSuffix(ln, "\r")
	}
	if n := len(out); n > 0 && out[n-1] == "" {
		out = out[:n-1]
	}
	return out
}

// nearbyLines 在文件内容里找与 target 首行最相近的行，返回该行前后各 2 行
// （带 "行号|" 前缀）。相近判定：与 target 首行（截 64 字节探针）的最长公共
// 子串 ≥ 探针长的 1/4 且 ≥4 字节——纯词法事实，不做任何语义猜测。
// 找不到返回空串（调用方明说"没有相近行"）。只读，不改文件。
func nearbyLines(fileContent, target string) string {
	first := target
	if i := strings.IndexByte(target, '\n'); i >= 0 {
		first = target[:i]
	}
	first = strings.TrimRight(first, "\r")
	const probeMax = 64
	if len(first) > probeMax {
		first = first[:probeMax]
	}
	if first == "" {
		return ""
	}
	lines := splitLines(fileContent)
	best, bestScore := -1, 0
	for i, ln := range lines {
		if s := commonRunLen(ln, first); s > bestScore {
			bestScore, best = s, i
		}
	}
	if best < 0 || bestScore < 4 || bestScore*4 < len(first) {
		return ""
	}
	lo := best - 2
	if lo < 0 {
		lo = 0
	}
	hi := best + 3
	if hi > len(lines) {
		hi = len(lines)
	}
	var b strings.Builder
	for i := lo; i < hi; i++ {
		fmt.Fprintf(&b, "%d|%s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n")
}

// commonRunLen 返回 a 与 b 的最长公共子串长度（两串都截 256 字节，成本有界；
// 只用于错误提示的邻近行判定，不需要精确算法）。
func commonRunLen(a, b string) int {
	if len(a) > 256 {
		a = a[:256]
	}
	if len(b) > 256 {
		b = b[:256]
	}
	best := 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best = k
			}
		}
	}
	return best
}

// diffContentLimit 是模型可见的短 diff 字节预算（模型侧已有 4096 的工具结果
// 上限——diff 占小头，正文/错误信息才有空间）。
const diffContentLimit = 1200

// withShortDiff 把短 diff 拼进给模型的 Content（0.0.06：模型不能再只看到
// "written N bytes"——它需要 diff 确认自己改了什么）。超长走头尾保留截断
// （中间标注丢失字节数），并注明该文件的完整预览在界面工具卡里。
func withShortDiff(content, path, diff string) string {
	if diff == "" {
		return content
	}
	short := tools.HeadTail(diff, diffContentLimit)
	if len(diff) > diffContentLimit {
		short += fmt.Sprintf("\n(full diff for %s omitted here; see the tool card preview)", path)
	}
	return content + "\n" + short
}

const listLimit = 500

// treeDepth / treeLimit 是 tree 的边界（0.0.06）：深度最多 2 层、条目总数与
// list 同量级（500）——树是有界的骨架，不是无限递归的目录 dump。
const (
	treeDepth = 2
	treeLimit = listLimit
)

// tree 输出有界目录骨架：从 path（缺省根）向下最多 treeDepth 层，条目总数
// treeLimit 封顶（超出标注 truncated）。越界路径走 resolve 拒绝（与 read/list
// 同一守卫）。输出形态：目录带尾部 /、按"目录优先 + 字典序"排序、两空格缩进。
func (t *Tool) tree(path string) (tools.ToolResult, error) {
	if path == "" {
		path = "."
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("tree failed: %v", err), nil
	}
	if !info.IsDir() {
		return bizErrf("not a directory: %s", path), nil
	}
	var b strings.Builder
	count := 0
	truncated := false
	var walk func(dir string, rel string, depth int) error
	walk = func(dir, rel string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // 单个目录读失败不整次失败（权限等），骨架继续
		}
		// 忽略目录过滤（0.0.21）：tree 会下钻子目录，不过滤会把 node_modules
		// 的一级子目录清单整层吐进来（500 条上限全是垃圾）。
		entries = filterIgnored(entries)
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		for _, e := range entries {
			if count >= treeLimit {
				truncated = true
				return errStopTree
			}
			indent := strings.Repeat("  ", depth)
			name := e.Name()
			if e.IsDir() {
				fmt.Fprintf(&b, "%s%s/\n", indent, name)
			} else {
				fmt.Fprintf(&b, "%s%s\n", indent, name)
			}
			count++
			if e.IsDir() && depth+1 < treeDepth {
				childRel := name
				if rel != "" && rel != "." {
					childRel = rel + "/" + name
				}
				if err := walk(filepath.Join(dir, e.Name()), childRel, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(full, ".", 0); err != nil && err != errStopTree {
		return bizErrf("tree failed: %v", err), nil
	}
	if count == 0 {
		return tools.ToolResult{Content: "empty directory"}, nil
	}
	out := b.String()
	if truncated {
		out += fmt.Sprintf("…（truncated at %d entries; use list on a subdirectory for more）", treeLimit)
	}
	return tools.ToolResult{Content: out}, nil
}

var errStopTree = errors.New("tree stop")

// filterIgnored 剔除默认忽略目录（0.0.21）：只对目录生效——IgnoredDir 的语义是
// "目录名"（.git/node_modules 等），同名文件是合法内容，不误杀。
// 单一来源在 platform/workspace（search 与 @ 引用早已接上），list/tree 是漏网的两处。
func filterIgnored(entries []os.DirEntry) []os.DirEntry {
	out := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && workspace.IgnoredDir(e.Name()) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (t *Tool) list(path string) (tools.ToolResult, error) {
	if path == "" {
		path = "."
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	if !info.IsDir() {
		return bizErrf("not a directory: %s", path), nil
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	// 忽略目录过滤（0.0.21）：与 search / @ 引用同一份清单（platform/workspace 单一来源）
	// ——此前 list 漏接，模型在大项目列根目录会被 node_modules 等淹没。
	entries = filterIgnored(entries)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	total := len(entries)
	if total == 0 {
		return tools.ToolResult{Content: "empty directory"}, nil
	}
	var b strings.Builder
	n := total
	if n > listLimit {
		n = listLimit
	}
	for i := 0; i < n; i++ {
		e := entries[i]
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "dir  %s/\n", name)
			continue
		}
		size := "-"
		if fi, err := e.Info(); err == nil {
			size = fmt.Sprintf("%d", fi.Size())
		}
		fmt.Fprintf(&b, "file %s  %s\n", name, size)
	}
	if total > listLimit {
		fmt.Fprintf(&b, "(truncated, showing %d of %d entries)\n", listLimit, total)
	}
	return tools.ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// bizErrf 构造模型可见的业务失败（ToolResult.IsError，而非机制 error）。
func bizErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

// fsTitle 卡片主标签：**工作区相对路径**（分隔符统一正斜杠）。
// 为什么不再只取末段：末段在多目录同名文件（agent.go）下无法区分，「本轮变更」
// 列表会挤出一排同名条目；相对路径是唯一能区分目标的稳定标签，长度问题由 UI
// 做两段式省略（目录暗、末段亮）。根/空路径回退 "(workspace)"。
func fsTitle(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "(workspace)"
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || p == "/" {
		return "(workspace)"
	}
	return p
}

// fsOp 卡片动作徽章：replace 的语义即"编辑"
func fsOp(action string) string {
	if action == "replace" {
		return "edit"
	}
	return action
}

func bizErr(err error) tools.ToolResult {
	return tools.ToolResult{Content: err.Error(), IsError: true}
}
