package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tiancode/internal/core/agent"
)

// 0.0.06：每个执行步骤的系统说明必须带三行环境事实——操作系统、实际 shell
// （Windows 为 cmd.exe，不是 PowerShell）、本会话工作区绝对路径；空工作区写明
// 纯对话。三行是事实不是守则；绝不包含任何密钥。
func TestChatService_SessionFactsPreface(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	ag := agent.NewLoop(nil, "m", nil)

	// 有工作区：三行齐 + 绝对路径在场
	root := t.TempDir()
	if err := s.applyExtensionPreface(context.Background(), ag, root, false); err != nil {
		t.Fatal(err)
	}
	p := ag.Preface()
	if !strings.Contains(p, "操作系统") {
		t.Fatalf("缺操作系统行：%q", p)
	}
	if runtime.GOOS == "windows" && !strings.Contains(p, "cmd.exe") {
		t.Fatalf("Windows 必须写明实际 shell 是 cmd.exe：%q", p)
	}
	if runtime.GOOS != "windows" && !strings.Contains(p, "sh") {
		t.Fatalf("Unix 必须写明实际 shell：%q", p)
	}
	if !strings.Contains(p, root) {
		t.Fatalf("缺工作区绝对路径 %q：%q", root, p)
	}
	lower := strings.ToLower(p)
	for _, secret := range []string{"sk-", "api_key", "apikey", "bearer "} {
		if strings.Contains(lower, secret) {
			t.Fatalf("preface 不得包含密钥类内容（%s）：%q", secret, p)
		}
	}

	// 空工作区：写明纯对话、没有本地文件工具
	ag2 := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag2, "", false); err != nil {
		t.Fatal(err)
	}
	p2 := ag2.Preface()
	if !strings.Contains(p2, "纯对话，没有本地文件工具") {
		t.Fatalf("空工作区必须写明纯对话：%q", p2)
	}
	if strings.Contains(p2, "本会话工作区：D:") {
		t.Fatalf("空工作区不得出现具体路径：%q", p2)
	}
}

// 0.0.42（C-PRE-1）：工作区根 AGENTS.md 作为项目规则注入；无/空文件零噪声；
// 超上限头尾保留并注明节选。
func TestSessionFacts_AgentsRules(t *testing.T) {
	root := t.TempDir()
	if got := sessionFacts(root); strings.Contains(got, "项目规则") {
		t.Fatalf("无 AGENTS.md 不应出现规则段：%q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("# 规则\n- 构建用 npm run build\n- 禁止改 vendor"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := sessionFacts(root)
	if !strings.Contains(got, "## 项目规则") || !strings.Contains(got, "禁止改 vendor") {
		t.Fatalf("AGENTS.md 应注入：%q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sessionFacts(root); strings.Contains(got, "项目规则") {
		t.Fatalf("空 AGENTS.md 不应出现规则段：%q", got)
	}

	big := strings.Repeat("A", agentsRulesLimit+1000) + "尾部标记" + strings.Repeat("B", 500)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	got = sessionFacts(root)
	if !strings.Contains(got, "已节选") || !strings.Contains(got, "尾部标记") {
		t.Fatalf("超限应节选并保尾部：%q", got[max(0, len(got)-160):])
	}
	if len(got) > agentsRulesLimit+600 { // 环境三行 + 上限正文 + 标注的粗上界
		t.Fatalf("注入体量应受限：len=%d", len(got))
	}
}

// 0.0.43（C-APP-6）：AGENTS.md frontmatter 的 check 命令注入环境事实；
// 无 frontmatter / 无文件 = 零噪声。
func TestSessionFacts_AgentsCheckCommand(t *testing.T) {
	root := t.TempDir()
	if got := sessionFacts(root); strings.Contains(got, "项目检查命令") {
		t.Fatalf("无声明不应出现检查命令行：%q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("---\ncheck: npm run build\n---\n\n# 规则\n- 用 pnpm"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := sessionFacts(root)
	if !strings.Contains(got, "项目检查命令：`npm run build`") {
		t.Fatalf("check 命令应注入事实：%q", got)
	}
	// 规则正文与命令同场注入
	if !strings.Contains(got, "用 pnpm") {
		t.Fatalf("规则正文应保留：%q", got)
	}
}

// 0.0.44（C-EXT-1）：工作区技能——<root>/.tiancode/skills/*.md 注入技能清单，
// skill 工具能取到正文；同名技能工作区覆盖全局。
func TestWorkspaceSkills_MergedIntoPrefaceAndTool(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tiancode", "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	wsSkill := "---\nname: 发布流程\ndescription: 本项目的发版步骤\n---\n第一步：跑 release.ps1"
	if err := os.WriteFile(filepath.Join(dir, "release.md"), []byte(wsSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	// 同名全局技能：应被工作区覆盖
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "extensions.json")
	if err := os.WriteFile(globalPath, []byte(`{"skills":[{"id":"g1","name":"发布流程","description":"全局旧版","body":"过时的步骤","enabled":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newChannelService(t, Config{ExtensionsPath: globalPath})
	defer s.Close()

	ag := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag, root, false); err != nil {
		t.Fatal(err)
	}
	p := ag.Preface()
	if !strings.Contains(p, "发布流程") || !strings.Contains(p, "本项目的发版步骤") {
		t.Fatalf("工作区技能应进清单：%q", p)
	}
	if strings.Contains(p, "全局旧版") {
		t.Fatalf("同名全局技能应被覆盖：%q", p)
	}

	// skill 工具取到的是工作区正文
	file := s.mergedSkills(root)
	found := false
	for _, sk := range file.Skills {
		if sk.Name == "发布流程" {
			found = true
			if !strings.Contains(sk.Body, "release.ps1") {
				t.Fatalf("工作区正文应覆盖全局：%q", sk.Body)
			}
		}
	}
	if !found {
		t.Fatalf("合并清单应含工作区技能：%+v", file.Skills)
	}

	// 无目录：零噪声
	if got, err := workspaceSkills(t.TempDir()); err != nil || got != nil {
		t.Fatalf("无技能目录应零噪声：%v %v", got, err)
	}
}

// 解析纪律：frontmatter 缺省回退文件名、空文件丢弃、体量有界。
func TestWorkspaceSkills_ParseDiscipline(t *testing.T) {
	sk := parseWorkspaceSkill("debug.md", []byte("直接正文，无 frontmatter"))
	if sk.Name != "debug" || !strings.Contains(sk.Body, "直接正文") {
		t.Fatalf("无 frontmatter 应回退文件名：%+v", sk)
	}
	if got := parseWorkspaceSkill("empty.md", []byte("   \n")); got.Name != "" {
		t.Fatalf("空文件应丢弃：%+v", got)
	}
	big := strings.Repeat("长", wsSkillMaxBody+100)
	got := parseWorkspaceSkill("big.md", []byte(big))
	if len(got.Body) > wsSkillMaxBody+200 {
		t.Fatalf("正文应有界：%d", len(got.Body))
	}
}
