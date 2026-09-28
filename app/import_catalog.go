package app

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// importTextLimit 防止把超大文件整份灌进界面。
const importTextLimit = 256 * 1024

// importSkillCap 限制一次从目录扫到的 SKILL.md 数量。
const importSkillCap = 40

// skillFile 是一次导入读到的文本（目录导入时 Name 为相对路径）。
type skillFile struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// PickImport 用系统对话框导入主流配置。
// kind：mcp（mcp.json）| skill（单个 SKILL.md）| skill-dir（技能目录，递归找 SKILL.md）。
// 用户取消返回空串。目录结果是 {"files":[{"name","body"}]}。
func (b *Bind) PickImport(kind string) (string, error) {
	if b.AppCtx == nil {
		return "", errors.New("应用尚未就绪（缺少窗口上下文），无法打开选择框")
	}
	switch kind {
	case "mcp":
		return b.pickTextFile("选择 mcp.json（Claude / Cursor / VS Code）", "JSON (*.json)", "*.json")
	case "skill":
		return b.pickTextFile("选择 SKILL.md", "Markdown (*.md)", "*.md")
	case "skill-home":
		files, err := collectKnownSkills()
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "", errors.New("没有在用户目录找到 SKILL.md（已查找 .agents/skills、.claude/skills、.codex/skills）")
		}
		raw, err := json.Marshal(map[string]any{"files": files})
		if err != nil {
			return "", err
		}
		return string(raw), nil
	case "skill-dir":
		dir, err := wruntime.OpenDirectoryDialog(b.AppCtx, wruntime.OpenDialogOptions{
			Title: "选择技能目录（含 SKILL.md，可含多级子目录）",
		})
		if err != nil || strings.TrimSpace(dir) == "" {
			return "", err
		}
		files, err := collectSkillFiles(dir)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "", errors.New("这个目录里没有 SKILL.md")
		}
		raw, err := json.Marshal(map[string]any{"files": files})
		if err != nil {
			return "", err
		}
		return string(raw), nil
	default:
		return "", errors.New("不支持的导入类型：" + kind)
	}
}

func (b *Bind) pickTextFile(title, filterName, pattern string) (string, error) {
	path, err := wruntime.OpenFileDialog(b.AppCtx, wruntime.OpenDialogOptions{
		Title: title,
		Filters: []wruntime.FileFilter{{
			DisplayName: filterName,
			Pattern:     pattern,
		}},
	})
	if err != nil || strings.TrimSpace(path) == "" {
		return "", err
	}
	return readImportFile(path)
}

func readImportFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, importTextLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > importTextLimit {
		return "", errors.New("文件超过 256KB，请只导入配置或 SKILL.md")
	}
	return string(b), nil
}

func collectKnownSkills() ([]skillFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []skillFile
	for _, dir := range []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
	} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		got, err := collectSkillFiles(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
		if len(out) >= importSkillCap {
			out = out[:importSkillCap]
			break
		}
	}
	return out, nil
}

func collectSkillFiles(root string) ([]skillFile, error) {
	var out []skillFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == "node_modules" || name == ".git" || name == "vendor" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(name, "SKILL.md") {
			return nil
		}
		if len(out) >= importSkillCap {
			return filepath.SkipAll
		}
		body, err := readImportFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = name
		}
		out = append(out, skillFile{Name: filepath.ToSlash(rel), Body: body})
		return nil
	})
	return out, err
}
