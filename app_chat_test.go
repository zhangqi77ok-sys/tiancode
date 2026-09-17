package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/config"
)

type mockResolver struct {
	cfg *config.ChannelConfig
}
func (m *mockResolver) GetChannelForModel(reqModel string) *config.ChannelConfig {
	return m.cfg
}

func TestResolveChatCredentials_UsesPrimary(t *testing.T) {
	primary := &config.ChannelConfig{
		Endpoint: "https://example.invalid/v1",
		APIKey:   "fake-api-key-0123456789abcdef",
		Model:    "local-model",
	}
	ep, key, model, _, err := resolveChatCredentials(&mockResolver{cfg: primary}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ep != "https://example.invalid/v1" || key == "" || model != "local-model" {
		t.Fatalf("got %s %s %s", ep, key, model)
	}
	_, _, model, _, err = resolveChatCredentials(&mockResolver{cfg: primary}, "override")
	if err != nil || model != "override" {
		t.Fatalf("override model: %s %v", model, err)
	}
}

func TestAppendEnabledPolicies_SkipsDisabledAndEmpty(t *testing.T) {
	out := appendEnabledPolicies("base", []config.SkillConfig{
		{Name: "off", Prompt: "x", Enabled: false},
		{Name: "go-style", Prompt: "prefer go test", Enabled: true},
		{Name: "empty", Prompt: "  ", Enabled: true},
	}, []config.RuleConfig{
		{Title: "r1", Content: "no fake data", Enabled: true},
	})
	if !strings.Contains(out, "[技能 go-style] prefer go test") {
		t.Fatalf("missing skill: %s", out)
	}
	if strings.Contains(out, "off") || strings.Contains(out, "[技能 empty]") {
		t.Fatalf("disabled/empty skill leaked: %s", out)
	}
	if !strings.Contains(out, "[规则规约] no fake data") {
		t.Fatalf("missing rule: %s", out)
	}
}

func TestApp_ImportSkillMarkdown(t *testing.T) {
	app := NewApp()
	tmpDir, err := os.MkdirTemp("", "tcode_skill_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. 带 YAML frontmatter 的标准 SKILL.md
	skill1File := filepath.Join(tmpDir, "SKILL.md")
	skill1Content := `---
name: my-spec-skill
description: elite architecture reviewer
---
You must follow strict architectural guidelines.
`
	if err := os.WriteFile(skill1File, []byte(skill1Content), 0644); err != nil {
		t.Fatal(err)
	}

	imported1, err := app.ImportSkillMarkdown(skill1File)
	if err != nil {
		t.Fatalf("ImportSkillMarkdown frontmatter failed: %v", err)
	}
	if imported1.Name != "my-spec-skill" {
		t.Errorf("expected name my-spec-skill, got: %s", imported1.Name)
	}
	if imported1.Description != "elite architecture reviewer" {
		t.Errorf("expected description 'elite architecture reviewer', got: %s", imported1.Description)
	}
	if !strings.Contains(imported1.Prompt, "strict architectural guidelines") {
		t.Errorf("expected prompt to contain guidelines, got: %s", imported1.Prompt)
	}

	// 验证 ListSkills 包含该技能及 prompt
	skills := app.ListSkills()
	found := false
	for _, sk := range skills {
		if sk.Name == "my-spec-skill" && strings.Contains(sk.Prompt, "strict architectural guidelines") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListSkills() did not contain the imported skill")
	}

	// 验证新对话 system prompt 包含已启用技能的 prompt
	sysPrompt := appendEnabledPolicies("base", app.ListSkills(), app.ListRules())
	if !strings.Contains(sysPrompt, "[技能 my-spec-skill]") || !strings.Contains(sysPrompt, "strict architectural guidelines") {
		t.Fatalf("system prompt did not contain imported skill prompt: %s", sysPrompt)
	}

	// 2. 无 YAML frontmatter 的 Markdown 文件
	skill2File := filepath.Join(tmpDir, "plain_guidelines.md")
	skill2Content := "# Plain Skill\nAlways test before ship."
	if err := os.WriteFile(skill2File, []byte(skill2Content), 0644); err != nil {
		t.Fatal(err)
	}
	imported2, err := app.ImportSkillMarkdown(skill2File)
	if err != nil {
		t.Fatalf("ImportSkillMarkdown plain failed: %v", err)
	}
	if imported2.Name != "plain_guidelines" {
		t.Errorf("expected name plain_guidelines, got: %s", imported2.Name)
	}
	if !strings.Contains(imported2.Prompt, "Always test before ship") {
		t.Errorf("expected prompt to contain file content, got: %s", imported2.Prompt)
	}
}

func TestApp_ExpandMentionedFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_mention_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	readmeFile := filepath.Join(tmpDir, "README.md")
	readmeContent := "# Project Readme Title\nThis is the authentic project body."
	if err := os.WriteFile(readmeFile, []byte(readmeContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. 存在的文件被成功展开为文件内容
	prompt := "Please review @README.md and suggest changes."
	expanded := expandMentionedFiles(tmpDir, nil, prompt)
	if !strings.Contains(expanded, "# Project Readme Title") {
		t.Errorf("expected expanded prompt to contain readme content, got: %s", expanded)
	}
	if !strings.Contains(expanded, "[引用文件内容: README.md]") {
		t.Errorf("expected expanded prompt to contain reference header, got: %s", expanded)
	}

	// 2. 超过 8KB 的文件做安全截断
	largeFile := filepath.Join(tmpDir, "large.txt")
	largeContent := strings.Repeat("A123456789\n", 1000) // ~11KB
	if err := os.WriteFile(largeFile, []byte(largeContent), 0644); err != nil {
		t.Fatal(err)
	}
	promptLarge := "Check @large.txt please"
	expandedLarge := expandMentionedFiles(tmpDir, nil, promptLarge)
	if !strings.Contains(expandedLarge, "超长已截断，保留前 8KB") {
		t.Errorf("expected truncation notice for large file, got len: %d", len(expandedLarge))
	}

	// 3. 不存在的文件或纯技能名不触发报错，原样保留
	promptMissing := "Use @non_existent_skill for help"
	expandedMissing := expandMentionedFiles(tmpDir, nil, promptMissing)
	if expandedMissing != promptMissing {
		t.Errorf("expected untouched prompt for missing file, got: %s", expandedMissing)
	}
}

