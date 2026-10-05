package codeintel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol 是文件大纲的一行（带 1 起行号，模型可直接跳 read 分段）。
type Symbol struct {
	Line int
	Kind string // func / method / type / const / var / heading
	Text string // 已折叠为单行的签名或标题
}

// symbolLimit 是大纲条目上限：大纲是有界的骨架，不是全文件 dump。
const symbolLimit = 300

// Symbols 输出文件符号大纲。.go 走 go/parser（精确，签名取自源码）；
// 其余语言走正则启发式（条目 kind 标注启发式口径）。
// 绝不 panic：解析失败返回 nil + note 由调用方决定话术。
func Symbols(path string, src []byte) ([]Symbol, string) {
	if strings.HasSuffix(strings.ToLower(path), ".go") {
		ss, err := goSymbols(src)
		if err != nil {
			return nil, "Go 解析失败：" + err.Error()
		}
		return ss, ""
	}
	ss := regexSymbols(filepath.Ext(path), string(src))
	if ss == nil {
		return nil, "该扩展名暂无大纲引擎（Go 精确，ts/js/py/md 启发式）"
	}
	return ss, "（启发式，非语义精确）"
}

// goSymbols 用 parser 产出 .go 大纲：package、类型、函数/方法、顶层 const/var。
// 签名从源码切片（多行折叠为单行）——printer 打印裸 AST 节点会丢接收者，
// 源码切片是"模型在 read 里会看到的原文"，两者必须一致。
func goSymbols(src []byte) ([]Symbol, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil && f == nil {
		return nil, err
	}
	var out []Symbol
	out = append(out, Symbol{Line: fset.Position(f.Package).Line, Kind: "package", Text: f.Name.Name})
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			kind := "func"
			if decl.Recv != nil && len(decl.Recv.List) > 0 {
				kind = "method"
			}
			end := decl.End()
			if decl.Body != nil {
				end = decl.Body.Pos() // 函数只取签名，不带函数体
			}
			text := srcSpan(fset, src, decl.Pos(), end)
			if text == "?" && decl.Name != nil {
				// 坏函数（语法错误处）切片失效：AST 名字兜底，条目保留
				text = "func " + decl.Name.Name + "(?)"
			}
			out = append(out, Symbol{
				Line: fset.Position(decl.Pos()).Line,
				Kind: kind,
				Text: text,
			})
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					text := srcSpan(fset, src, sp.Pos(), sp.End())
					// Kind 已标 "type"，签名里的关键字去重（无括号形态 span 常含 "type"）
					text = strings.TrimPrefix(text, "type ")
					out = append(out, Symbol{
						Line: fset.Position(sp.Pos()).Line,
						Kind: "type",
						Text: text,
					})
				case *ast.ValueSpec:
					kind := "var"
					if decl.Tok == token.CONST {
						kind = "const"
					}
					names := make([]string, 0, len(sp.Names))
					for _, n := range sp.Names {
						names = append(names, n.Name)
					}
					out = append(out, Symbol{
						Line: fset.Position(sp.Pos()).Line,
						Kind: kind,
						Text: strings.Join(names, ", "),
					})
				}
			}
		}
		if len(out) >= symbolLimit {
			return out[:symbolLimit], nil
		}
	}
	return out, nil
}

// srcSpan 取源码 [from, to) 并折叠为单行（多行签名压平，空白归一）。
func srcSpan(fset *token.FileSet, src []byte, from, to token.Pos) string {
	s, e := fset.Position(from).Offset, fset.Position(to).Offset
	if s < 0 || e > len(src) || s >= e {
		return "?"
	}
	return strings.Join(strings.Fields(string(src[s:e])), " ")
}

// 正则启发式大纲：只覆盖常见语言的高置信形态（行首的函数/类/标题定义）。
// 误报可控——宁缺勿滥，匹配不上的文件明说"暂无大纲"。
var regexPatterns = map[string][]*regexp.Regexp{
	".ts":  sigPatterns,
	".tsx": sigPatterns,
	".js":  sigPatterns,
	".jsx": sigPatterns,
	".mjs": sigPatterns,
	".py":  {regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`), regexp.MustCompile(`^\s*class\s+(\w+)`)},
	".md":  {regexp.MustCompile(`^(#{1,4})\s+(.*)`)},
}

var sigPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*(\w+)`),
	regexp.MustCompile(`^\s*(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`),
	regexp.MustCompile(`^\s*(?:export\s+)?interface\s+(\w+)`),
	regexp.MustCompile(`^\s*(?:export\s+)?type\s+(\w+)\s*=`),
	regexp.MustCompile(`^\s*(?:export\s+)?enum\s+(\w+)`),
}

// regexSymbols 按扩展名跑启发式；无引擎返回 nil。
func regexSymbols(ext, src string) []Symbol {
	pats, ok := regexPatterns[strings.ToLower(ext)]
	if !ok {
		return nil
	}
	var out []Symbol
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		for pi, re := range pats {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			kind := "def"
			text := m[1]
			switch ext {
			case ".md":
				kind = "heading"
				text = strings.TrimLeft(line, "# ")
			case ".py":
				if pi == 1 {
					kind = "class"
				}
			default:
				if pi == 1 {
					kind = "class"
				}
			}
			out = append(out, Symbol{Line: i + 1, Kind: kind, Text: text})
			break
		}
		if len(out) >= symbolLimit {
			break
		}
	}
	return out
}
