package searchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newWS(t *testing.T) *Tool {
	t.Helper()
	return New(t.TempDir())
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSearch_PathEscapeRejected(t *testing.T) {
	tool := newWS(t)
	parent := filepath.Dir(tool.root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": "../evil"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("escape must be IsError")
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": parent}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("absolute escape must be IsError")
	}
}

func TestSearch_OutputBounded(t *testing.T) {
	tool := newWS(t)
	for i := 0; i < 80; i++ {
		write(t, tool.root, filepath.Join("src", fmt.Sprintf("%03d.go", i)), "needle here")
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 5}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	body := strings.TrimSuffix(res.Content, "\n")
	lines := strings.Split(body, "\n")
	matchLines := 0
	for _, ln := range lines {
		if strings.Contains(ln, "needle") && !strings.Contains(ln, "truncated") {
			matchLines++
		}
	}
	if matchLines != 5 {
		t.Fatalf("match lines = %d, want 5; %s", matchLines, res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Fatal("must annotate max_matches truncation")
	}
}

func TestSearch_OutputByteBounded(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "big.txt", strings.Repeat("x", 64*1024+1024)+"needle")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("byte-limit truncation must not be IsError: %+v", res)
	}
	if strings.TrimSpace(res.Content) == "no matches" || strings.Contains(res.Content, "no matches") {
		t.Fatalf("oversized first hit must not become no matches: %q", res.Content)
	}
	if !strings.Contains(res.Content, "truncated") || !strings.Contains(res.Content, "64KiB") {
		t.Fatalf("must annotate 64KiB truncation: %s", res.Content)
	}
}

func TestSearch_MaxMatchesExactNoTruncate(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "needle\nneedle")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 2}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "truncated") {
		t.Fatalf("exact max_matches with nothing left must not annotate: %s", res.Content)
	}
	body := strings.TrimSuffix(res.Content, "\n")
	matchLines := 0
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "needle") {
			matchLines++
		}
	}
	if matchLines != 2 {
		t.Fatalf("match lines = %d, want 2; %s", matchLines, res.Content)
	}
}

// R4：区外符号链接被跳过并在结果里汇总（不因一个链接让整次搜索失败）。
// Windows 创建文件符号链接需要特权：失败时跳过。
func TestSearch_SymlinkOutsideSkipped(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "leak.txt")
	if err := os.WriteFile(secret, []byte("HIT-OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak.txt")); err != nil {
		t.Skip("无法创建符号链接（需要特权）：", err)
	}
	write(t, root, "inside.txt", "HIT-INSIDE")
	tool := New(root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "HIT-OUTSIDE") {
		t.Fatalf("区外链接内容泄漏进结果：%s", res.Content)
	}
	if !strings.Contains(res.Content, "HIT-INSIDE") {
		t.Fatalf("区内内容应正常搜到：%s", res.Content)
	}
	if !strings.Contains(res.Content, "outside workspace") {
		t.Fatalf("应汇总被跳过的区外路径（可观测）：%s", res.Content)
	}
}

// 0.0.12：每条命中带前后各 2 行上下文；命中行 path:line:text、上下文行 path-line-text
// （rg 的 :/- 约定——模型不必再 read 一次才能改那个函数）。
func TestSearch_ContextLines(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "one\ntwo\nthree\nNEEDLE\nfive\nsix\nseven\n")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "NEEDLE"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	for _, want := range []string{"a.txt:4:NEEDLE", "a.txt-2-two", "a.txt-3-three", "a.txt-5-five", "a.txt-6-six"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("缺少 %q：\n%s", want, res.Content)
		}
	}
	// ±2 之外的行不得出现（否则就是把整个函数贴进来）
	for _, bad := range []string{"-1-one", "-7-seven", ":1:", ":7:"} {
		if strings.Contains(res.Content, bad) {
			t.Fatalf("上下文超界（%q）：\n%s", bad, res.Content)
		}
	}
}

// 相邻命中的上下文窗口合并成一块：同一行只输出一次。
func TestSearch_ContextMerged(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "b.txt", "l1\nHIT-a\nl3\nHIT-b\nl5\n")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	lines := strings.Split(strings.TrimRight(res.Content, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("合并后应恰好 5 行（不重复输出上下文）：%v", lines)
	}
	seen := map[string]int{}
	for _, ln := range lines {
		seen[ln]++
		if seen[ln] > 1 {
			t.Fatalf("重复输出 %q：\n%s", ln, res.Content)
		}
	}
	if !strings.Contains(res.Content, "b.txt:2:HIT-a") || !strings.Contains(res.Content, "b.txt:4:HIT-b") {
		t.Fatalf("两条命中都要标出（: 分隔）：\n%s", res.Content)
	}
}

// 上下文计入 64KiB 预算：超了在块边界收手——要么给完整块，要么不给。
func TestSearch_ContextCountsTowardByteBudget(t *testing.T) {
	tool := newWS(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		pad := strings.Repeat("c", 200)
		b.WriteString(pad + "\n") // 命中前 2 行
		b.WriteString(pad + "\n")
		b.WriteString(fmt.Sprintf("NEEDLE-%03d%s\n", i, pad))
		b.WriteString(pad + "\n") // 命中后 2 行
		b.WriteString(pad + "\n")
	}
	write(t, tool.root, "big.txt", b.String())
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "NEEDLE", "max_matches": 200}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if len(res.Content) > maxOutputBytes+4096 {
		t.Fatalf("输出必须受 64KiB 预算约束：%d 字节", len(res.Content))
	}
	if !strings.Contains(res.Content, "64KiB") {
		t.Fatalf("预算截断必须标注：\n%s", res.Content[len(res.Content)-200:])
	}
	// 不给半截块：每个被保留的命中块都应带齐 2 行前文（行首两块除外）与 2 行后文。
	if !strings.Contains(res.Content, "big.txt-2-"+strings.Repeat("c", 200)) {
		t.Fatalf("首个命中块的前文应完整保留")
	}
}

// 0.0.12：files_only 只按路径找文件——不读内容、只给路径（找 handler.go 不必 tree 逐层翻）。
func TestSearch_FilesOnlyByPath(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "internal/app/handler.go", "package app")
	write(t, tool.root, "internal/app/other.go", "package app // handler.go 只出现在内容里")
	write(t, tool.root, "vendor/skip/handler.go", "package skip")
	// 超过内容搜索的 1MiB 体积闸门：按名字找文件仍要能找到
	write(t, tool.root, "lib/generated_handler.go", strings.Repeat("x", 2<<20))
	res, err := tool.Execute(context.Background(), args(t, map[string]any{
		"pattern": `handler\.go$`, "files_only": true,
	}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "internal/app/handler.go") {
		t.Fatalf("按名字应命中：\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "lib/generated_handler.go") {
		t.Fatalf("大文件（不读内容）也该按名字找到：\n%s", res.Content)
	}
	if strings.Contains(res.Content, "other.go") {
		t.Fatalf("名字不匹配的不得出现（files_only 不看内容）：\n%s", res.Content)
	}
	if strings.Contains(res.Content, "vendor") {
		t.Fatalf("内置忽略目录仍要跳过：\n%s", res.Content)
	}
	if strings.Contains(res.Content, ":1:") || strings.Contains(res.Content, "-1-") {
		t.Fatalf("files_only 只给路径，不带行号：\n%s", res.Content)
	}
}

// files_only 的配额同样有效：超了截断并标注。
func TestSearch_FilesOnlyMaxMatches(t *testing.T) {
	tool := newWS(t)
	for i := 0; i < 10; i++ {
		write(t, tool.root, filepath.Join("pkg", fmt.Sprintf("m%02d.go", i)), "package pkg")
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{
		"pattern": `\.go$`, "files_only": true, "max_matches": 3,
	}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	got := strings.Count(res.Content, "pkg/m")
	if got != 3 {
		t.Fatalf("路径条数 = %d, want 3：\n%s", got, res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Fatalf("超配额要标注截断：\n%s", res.Content)
	}
}

func TestSearch_InvalidPattern(t *testing.T) {
	tool := newWS(t)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "["}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("%+v", res)
	}
}

func TestSearch_SkipsIgnoredAndBinary(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "lib/a.go", "HIT-lib")
	write(t, tool.root, "vendor/a.go", "HIT-vendor")
	write(t, tool.root, "bin/a.go", "HIT-bin")
	if err := os.WriteFile(filepath.Join(tool.root, "lib", "blob.bin"), []byte("HIT-\x00binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "lib/a.go") && !strings.Contains(res.Content, `lib\a.go`) {
		t.Fatalf("missing lib hit: %s", res.Content)
	}
	if strings.Contains(res.Content, "vendor") || strings.Contains(res.Content, "HIT-bin") || strings.Contains(res.Content, "blob.bin") {
		t.Fatalf("ignored/binary leaked: %s", res.Content)
	}

	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-", "path": "vendor"}))
	if err != nil || res.IsError {
		t.Fatalf("explicit vendor: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-vendor") {
		t.Fatalf("explicit vendor root must be searched: %s", res.Content)
	}
}

func TestSearch_NoMatchIsSuccess(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "hello")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "zzz-nope"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "no matches") {
		t.Fatalf("%+v", res)
	}
}

// 0.2.37：工作区里只有指向区外的链接、零命中时，不得谎报 "no matches"——
// 跳过汇总就是答案本身（模型/用户需要知道"有内容但被安全策略跳过"）。
func TestSearch_ZeroHitWithSkipsKeepsSummary(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "code.txt")
	if err := os.WriteFile(secret, []byte("needle in outside code"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "outside-link.txt")); err != nil {
		t.Skip("无法创建符号链接（需要特权）：", err)
	}
	write(t, root, "a.txt", "nothing relevant here")
	tool := New(root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("跳过汇总不是错误：%+v", res)
	}
	if !strings.Contains(res.Content, "outside workspace") {
		t.Fatalf("零命中必须保留跳过汇总：%q", res.Content)
	}
	if strings.TrimSpace(res.Content) == "no matches" {
		t.Fatalf("只有区外链接时不得谎报 no matches：%q", res.Content)
	}
}

func TestSearch_TimeoutPartial(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < 40000; i++ {
		body.WriteString("needle line\n")
	}
	write(t, root, "big.txt", body.String())
	tool := NewWithTimeout(root, time.Nanosecond)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 200}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("want TimedOut, got %+v", res)
	}
	if res.Content == "" {
		t.Fatal("Content must be non-empty")
	}
	hasHit := strings.Contains(res.Content, "big.txt:")
	hasTimeout := strings.Contains(res.Content, "TIMEOUT")
	if !hasHit && !hasTimeout {
		t.Fatalf("want path:line: partial or TIMEOUT, got %q", res.Content)
	}
}

// 并行扫描的确定性：多目录夹具、大小文件混排（worker 完成顺序与路径序必然错开），
// 多次执行输出逐字节一致，且命中按工作区相对路径升序产出。大文件放在路径序最前
// 但扫描最慢：若产出跟着完成顺序走，首个命中就会现形。
func TestSearch_ParallelDeterministicOutput(t *testing.T) {
	root := t.TempDir()
	// 6 个目录 × 5 个小文件（各 1 命中）
	for _, d := range []string{"a", "b", "c", "a/sub", "b/deep/inner", "m/n/o/p"} {
		for i := 0; i < 5; i++ {
			rel := filepath.ToSlash(filepath.Join(d, fmt.Sprintf("f%d.txt", i)))
			write(t, root, filepath.FromSlash(rel), "NEEDLE "+rel+"\nfiller\n")
		}
	}
	// 路径序最小、扫描最慢的文件：20 个间隔命中的大文件（命中拉开才成块，
	// 连片命中会并成一个超配额的整块）
	var big strings.Builder
	for i := 0; i < 20; i++ {
		big.WriteString("NEEDLE big\n")
		for j := 0; j < 5; j++ {
			big.WriteString("filler\n")
		}
	}
	write(t, root, filepath.FromSlash("a/0big.txt"), big.String())

	tool := New(root)
	var first string
	for run := 0; run < 6; run++ {
		// 配额 30 < 总命中 50：截断点落在夹具内部，产出集合 = 路径序前 30 条
		res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "NEEDLE", "max_matches": 30}))
		if err != nil || res.IsError {
			t.Fatalf("run %d: %v %s", run, err, res.Content)
		}
		if run == 0 {
			first = res.Content
			continue
		}
		if res.Content != first {
			t.Fatalf("第 %d 次执行输出与第 1 次不一致——并行产出不确定", run+1)
		}
	}
	var hitPaths []string
	for _, ln := range strings.Split(first, "\n") {
		if !strings.Contains(ln, "NEEDLE") {
			continue // 上下文行/标注行不算命中
		}
		if i := strings.Index(ln, ":"); i > 0 {
			hitPaths = append(hitPaths, ln[:i])
		}
	}
	if len(hitPaths) != 30 {
		t.Fatalf("命中行应为 30（配额上限），实为 %d", len(hitPaths))
	}
	if !sort.SliceIsSorted(hitPaths, func(i, j int) bool { return hitPaths[i] < hitPaths[j] }) {
		t.Fatalf("命中未按路径升序产出：%v", headOf(hitPaths, 10))
	}
	if hitPaths[0] != "a/0big.txt" {
		t.Fatalf("首个命中应为 a/0big.txt（路径序最小且最慢），实为 %q", hitPaths[0])
	}
}

// 并行扫描的正确性基线：多目录下每文件恰一命中——全部命中、无遗漏、无重复。
func TestSearch_ParallelMultiDirCorrectness(t *testing.T) {
	root := t.TempDir()
	want := map[string]bool{}
	for _, d := range []string{"x", "y", "x/deep", "y/z/w"} {
		for i := 0; i < 8; i++ {
			rel := filepath.ToSlash(filepath.Join(d, fmt.Sprintf("g%d.go", i)))
			want[rel] = true
			write(t, root, filepath.FromSlash(rel), "package p\nMARK-"+rel+"\n")
		}
	}
	tool := New(root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": `MARK-\S+`}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	got := map[string]bool{}
	for _, ln := range strings.Split(res.Content, "\n") {
		if i := strings.Index(ln, ":"); i > 0 {
			got[ln[:i]] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("命中文件数 = %d, want %d：%v", len(got), len(want), res.Content)
	}
	for rel := range want {
		if !got[rel] {
			t.Fatalf("漏掉 %s：\n%s", rel, res.Content)
		}
	}
}

// 统一忽略清单（单一来源 internal/platform/workspace）：search 与壳层 @ 引用共用
// 同一份定稿——build/obj/.idea/.vscode 这些壳层原有而 search 漏掉的目录不再扫进
// 结果；显式 path 进忽略目录仍可搜（与 vendor 同规则，C-SEARCH-4）。
func TestSearch_SkipsUnifiedIgnoreList(t *testing.T) {
	tool := newWS(t)
	for _, d := range []string{"build", "obj", ".idea", ".vscode", ".GIT"} {
		write(t, tool.root, filepath.Join(d, "a.txt"), "HIT-"+strings.TrimPrefix(d, "."))
	}
	write(t, tool.root, filepath.Join("src", "a.txt"), "HIT-src")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-src") {
		t.Fatalf("普通目录应命中：\n%s", res.Content)
	}
	for _, d := range []string{"build", "obj", ".idea", ".vscode", ".GIT"} {
		if strings.Contains(res.Content, "HIT-"+strings.TrimPrefix(d, ".")) {
			t.Fatalf("统一忽略目录 %s 泄漏进结果：\n%s", d, res.Content)
		}
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-", "path": "build"}))
	if err != nil || res.IsError {
		t.Fatalf("explicit build: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-build") {
		t.Fatalf("显式指定忽略目录应仍可搜：\n%s", res.Content)
	}
}
