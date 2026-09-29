package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"tiancode/internal/core/session"
)

// IncomingAttachment 是前端发来的待发送附件（0.0.10）。
// DataB64：剪贴板截图 / 无路径的临时文件内容；磁盘文件走 SourcePath（不复制）。
type IncomingAttachment struct {
	Kind       string `json:"kind"` // image | file
	Name       string `json:"name"`
	MediaType  string `json:"mediaType"`  // 前端给的类型；图片按内容嗅探覆写
	DataB64    string `json:"dataB64"`    // 可空（磁盘文件）
	SourcePath string `json:"sourcePath"` // 用户磁盘上的绝对路径（可空）
}

// 附件硬约束（0.0.10）：图片单张 ≤5MB、一次 ≤4 张；非图片文件一次 ≤8 个；
// 文本 ≤256KB 内联进消息，>256KB 或二进制只附路径（>5MB 只报路径，不进上下文）。
const (
	maxImageBytes    = 5 << 20
	maxInlineBytes   = 256 << 10
	maxFilePathBytes = 5 << 20
	maxImages        = 4
	maxFiles         = 8
)

// materializeAttachments 校验并落位附件，返回账本引用形态。
func (s *ChatService) materializeAttachments(sessionID string, raw []IncomingAttachment) ([]session.UserAttachment, error) {
	var out []session.UserAttachment
	images, files := 0, 0
	attDir := session.AttachmentDir(s.cfg.DataDir, sessionID)
	for i, a := range raw {
		var data []byte
		var absPath string
		switch {
		case strings.TrimSpace(a.SourcePath) != "":
			abs, err := filepath.Abs(a.SourcePath)
			if err != nil {
				return nil, fmt.Errorf("附件 %d 路径非法：%w", i+1, err)
			}
			b, err := os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("读取附件 %s 失败：%w", a.Name, err)
			}
			data, absPath = b, abs
		case strings.TrimSpace(a.DataB64) != "":
			b, err := base64.StdEncoding.DecodeString(a.DataB64)
			if err != nil {
				return nil, fmt.Errorf("附件 %s 解码失败：%w", a.Name, err)
			}
			data = b
		default:
			return nil, fmt.Errorf("附件 %s 没有内容", a.Name)
		}

		if a.Kind == "image" {
			images++
			if images > maxImages {
				return nil, fmt.Errorf("图片最多 %d 张（第 %d 张被拒绝）", maxImages, images)
			}
			if len(data) > maxImageBytes {
				return nil, fmt.Errorf("图片 %s 超过 5MB 上限", a.Name)
			}
			media := sniffImageType(data)
			if media == "" {
				return nil, fmt.Errorf("附件 %s 不是受支持的图片（png/jpeg/gif/webp）", a.Name)
			}
			// 图片必须落附件目录（相对路径引用），重放时仍能带图
			name := fmt.Sprintf("img-%d-%s", images, a.Name)
			if err := os.MkdirAll(attDir, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(attDir, name), data, 0o600); err != nil {
				return nil, err
			}
			out = append(out, session.UserAttachment{
				Kind: "image", Name: a.Name, MediaType: media,
				Path:   filepath.ToSlash(filepath.Join("att", sessionID, name)),
				Inline: "none", Size: int64(len(data)),
			})
			continue
		}

		// 非图片文件
		files++
		if files > maxFiles {
			return nil, fmt.Errorf("文件最多 %d 个（第 %d 个被拒绝）", maxFiles, files)
		}
		inline := "path"
		if len(data) <= maxInlineBytes && isTextual(data) {
			inline = "full"
		}
		ua := session.UserAttachment{Kind: "file", Name: a.Name, MediaType: a.MediaType, Inline: inline, Size: int64(len(data))}
		if inline == "full" {
			// 内联：内容直接进消息文本，不需要存副本——但重放仍要能读，
			// 且需求说"不要为了预览把原件再存一份"：内联内容以附件目录副本为准
			name := fmt.Sprintf("file-%d-%s", files, a.Name)
			if err := os.MkdirAll(attDir, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(attDir, name), data, 0o600); err != nil {
				return nil, err
			}
			ua.Path = filepath.ToSlash(filepath.Join("att", sessionID, name))
		} else if absPath != "" {
			// 用户磁盘文件：记绝对路径，不复制原件
			if len(data) > maxFilePathBytes {
				return nil, fmt.Errorf("文件 %s 超过 5MB，已拒绝", a.Name)
			}
			ua.Path = absPath
		} else {
			// 剪贴板来的临时文件（无路径）：写入附件目录
			name := fmt.Sprintf("file-%d-%s", files, a.Name)
			if err := os.MkdirAll(attDir, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(attDir, name), data, 0o600); err != nil {
				return nil, err
			}
			ua.Path = filepath.ToSlash(filepath.Join("att", sessionID, name))
		}
		out = append(out, ua)
	}
	return out, nil
}

// atRefPattern 匹配消息文本里的 @路径 token（0.0.11）：行首或空白后的 @，
// 到下一个空白为止。与前端输入框 parseAtToken 的触发规则一致。
var atRefPattern = regexp.MustCompile(`(?:^|\s)@([^\s]+)`)

// resolveAtReferences 把消息文本中的 @相对路径 引用解析成附件（0.0.11）。
// 为什么在这里而不是前端展开：附件物化（大小上限/嗅探/落位/重放）只有后端有；
// 前端只负责让用户选得省力（菜单），内容进不进上下文由发送链路统一决定。
// 规则：
//   - 只认相对路径，且解析后必须仍在本会话工作区内（.. 越界跳过）——@ 不成为
//     绕过工具根边界的口子；
//   - 文件必须存在（目录/不存在/坏链接一律跳过，原文保留——用户可能就在谈论
//     "@ 路径怎么写"而不是引用文件）；
//   - 每条引用复用 materializeAttachments 的磁盘文件路径：校验、嗅探、落位、
//     重放全同附件链路（图片转 image 引用，文本 ≤256KB 内联）。
func (s *ChatService) resolveAtReferences(sessionID, root, text string) ([]session.UserAttachment, error) {
	root = strings.TrimSpace(root)
	if root == "" || !strings.Contains(text, "@") {
		return nil, nil
	}
	matches := atRefPattern.FindAllStringSubmatch(text, 8) // 上限 8 条：@ 是人手打的，防极端刷量
	if len(matches) == 0 {
		return nil, nil
	}
	var raw []IncomingAttachment
	for _, m := range matches {
		tok := m[1]
		tok = strings.Trim(tok, `"'(),.:;`) // 去掉贴着路径的中文/英文标点
		if tok == "" {
			continue
		}
		if filepath.IsAbs(tok) {
			continue // 只认相对路径：绝对路径引用不走 @（边界一致）
		}
		abs := filepath.Join(root, filepath.FromSlash(tok))
		// 越界守卫：解析结果必须仍在工作区内（filepath.Rel 回到 root 而非 ..）
		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, statErr := os.Stat(abs)
		if statErr != nil || info.IsDir() {
			continue
		}
		kind := "file"
		if isImageExt(tok) {
			kind = "image"
		}
		// Name 落盘时会拼进附件文件名：子路径分隔符必须拍平（docs/note.md → docs_note.md）
		safeName := strings.ReplaceAll(tok, "/", "_")
		safeName = strings.ReplaceAll(safeName, "\\", "_")
		raw = append(raw, IncomingAttachment{Kind: kind, Name: safeName, SourcePath: abs})
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return s.materializeAttachments(sessionID, raw)
}

// isImageExt 按扩展名粗判图片（内容嗅探在 materializeAttachments 里做，这里只分流 kind）。
func isImageExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// sniffImageType 按文件头嗅探图片类型（不只信扩展名）。
func sniffImageType(b []byte) string {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(b) >= 3 && bytes.Equal(b[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case len(b) >= 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
}

// isTextual 判断内容是否文本（前 8KB 无 NUL 且合法 UTF-8/ASCII）。
func isTextual(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	if bytes.IndexByte(b[:n], 0) >= 0 {
		return false
	}
	return utf8.Valid(b[:n]) || isMostlyASCII(b[:n])
}

func isMostlyASCII(b []byte) bool {
	return utf8.Valid(bytes.ToValidUTF8(b, []byte{'?'})) && !bytes.Contains(b, []byte{0xC0})
}
