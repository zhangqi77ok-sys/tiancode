// 工作区级技能（0.0.44）：`<root>/.tiancode/skills/*.md`，随仓库走的项目私有技能。
//
// 文件形态（与 SKILL.md 惯例同源）：frontmatter 声明 name/description，正文即技能全文：
//
//	---
//	name: 发布流程
//	description: tiancode 的发版步骤
//	---
//	正文……
//
// name 缺省用文件名（去 .md）；同名技能**工作区覆盖全局**（项目约定优先于个人收藏）。
// 纪律：有界（文件数与单文件体量都有上限）——技能清单进每轮系统说明，无界的
// 目录会撑爆上下文；读取失败按"没有这个技能"处理（技能是增强，不打断对话）。
package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tiancode/internal/platform/catalog"
)

const (
	wsSkillsDir      = ".tiancode/skills"
	wsSkillMaxFiles  = 50
	wsSkillMaxBody   = 32 << 10
	wsSkillMaxHeader = 4 << 10
)

// workspaceSkills 读取工作区技能目录；目录不存在返回 nil（零噪声）。
func workspaceSkills(root string) ([]catalog.Skill, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	dir := filepath.Join(root, filepath.FromSlash(wsSkillsDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []catalog.Skill
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(strings.ToLower(filepath.Ext(e.Name())), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // 产出确定性：技能清单顺序只由文件名决定
	for _, name := range names {
		if len(out) >= wsSkillMaxFiles {
			break
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // 单个文件读不了：跳过（技能是增强）
		}
		sk := parseWorkspaceSkill(name, b)
		if sk.Name != "" {
			out = append(out, sk)
		}
	}
	return out, nil
}

// parseWorkspaceSkill 解析单个技能文件：frontmatter 取 name/description，其余为正文。
// frontmatter 缺失或空 name → 文件名兜底；空文件（无正文无描述）→ 空 Skill（调用方丢弃）。
func parseWorkspaceSkill(filename string, data []byte) catalog.Skill {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	text := string(data)
	if len(text) > wsSkillMaxBody+wsSkillMaxHeader {
		text = text[:wsSkillMaxBody+wsSkillMaxHeader]
	}
	name, desc, body := base, "", text
	if strings.HasPrefix(strings.TrimSpace(text), "---") {
		lines := strings.Split(text, "\n")
		end := -1
		for i := 1; i < len(lines) && i < 40; i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end > 0 {
			for _, line := range lines[1:end] {
				key, value, found := strings.Cut(strings.TrimSpace(line), ":")
				if !found {
					continue
				}
				switch strings.TrimSpace(key) {
				case "name":
					if v := strings.TrimSpace(value); v != "" {
						name = v
					}
				case "description":
					desc = strings.TrimSpace(value)
				}
			}
			body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
		}
	}
	body = strings.TrimSpace(body)
	if len(body) > wsSkillMaxBody {
		body = body[:wsSkillMaxBody] + "\n（超出上限已截断；完整内容见 " + wsSkillsDir + "/" + filename + "）"
	}
	if name == "" || (body == "" && desc == "") {
		return catalog.Skill{} // 调用方按"解析失败"丢弃
	}
	return catalog.Skill{ID: "ws:" + filename, Name: name, Description: desc, Body: body, Enabled: true}
}

// mergeSkills 把工作区技能并进全局清单：同名覆盖（项目约定优先），其余追加。
// 返回新清单（不改传入值）；顺序 = 全局在前、工作区新增按文件名序在后。
func mergeSkills(global, workspace []catalog.Skill) []catalog.Skill {
	if len(workspace) == 0 {
		return global
	}
	out := make([]catalog.Skill, 0, len(global)+len(workspace))
	index := map[string]int{}
	for _, sk := range global {
		index[sk.Name] = len(out)
		out = append(out, sk)
	}
	for _, sk := range workspace {
		if i, ok := index[sk.Name]; ok {
			out[i] = sk // 工作区覆盖全局（同名）
			continue
		}
		index[sk.Name] = len(out)
		out = append(out, sk)
	}
	return out
}
