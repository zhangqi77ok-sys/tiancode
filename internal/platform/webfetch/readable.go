package webfetch

// 本文件是 webfetch 的"HTML → 可读正文"与字符集解码。
// 刻意不引第三方 HTML 解析器：这里只做"剥噪声取正文"，一个单向扫描的状态机
// 足够，换来的是一页读完的实现；为此扩大 vendor 依赖面不划算（仓库纪律）。

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// decodeBody 把响应体解码为 UTF-8 文本。
// 字符集策略（对齐 shelltool.decodeConsoleOutput 的"宁可乱码可见，不静默造数据"）：
// Content-Type 显式声明的字符集优先；未声明、或声明了我们处理不了的字符集时，
// 合法 UTF-8 原样通过，否则按 GBK 解码，仍解不开原样返回。
func decodeBody(contentType string, body []byte) string {
	if enc := declaredEncoding(contentType); enc != nil {
		if out, err := enc.NewDecoder().Bytes(body); err == nil {
			return string(out)
		}
		// 声明了却解不开：落到通用策略，不静默造数据
	}
	if utf8.Valid(body) {
		return string(body)
	}
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
	if err != nil {
		return string(body)
	}
	return string(out)
}

// declaredEncoding 解析 Content-Type 的 charset 参数（大小写不敏感、容忍引号），
// 返回对应编码；无需特判时为 nil。中文网页的声明只可能是 GBK 家族
// （gb2312/gbk/gb18030），统一按 GBK 解——vendor 只收了 simplifiedchinese，
// 不为罕见字符集扩大依赖面（iso-8859-1 等走通用策略兜底）。
func declaredEncoding(contentType string) encoding.Encoding {
	for _, param := range strings.Split(contentType, ";")[1:] {
		low := strings.ToLower(strings.TrimSpace(param))
		name, ok := strings.CutPrefix(low, "charset=")
		if !ok {
			continue
		}
		switch strings.Trim(name, `"`) {
		case "gbk", "gb2312", "gb18030":
			return simplifiedchinese.GBK
		}
	}
	return nil
}

// isHTML 判断响应体是否按 HTML 提取正文：Content-Type 声明优先；类型缺失时按
// 内容特征兜底——不少本地 dev server 对页面响应不带类型。特征只认显式的
// doctype/<html，避免把含 "<" 的纯文本误当 HTML；声明了类型就按类型来（json/
// text/plain 一律原样透传），不做内容猜测。
func isHTML(contentType, text string) bool {
	media := contentType
	if i := strings.IndexByte(media, ';'); i >= 0 {
		media = media[:i]
	}
	media = strings.ToLower(strings.TrimSpace(media))
	if media != "" {
		return strings.Contains(media, "html")
	}
	head := text
	if len(head) > 512 {
		head = head[:512]
	}
	head = strings.ToLower(head)
	return strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html")
}

// skipTags 的内容整体丢弃：脚本/样式是纯噪声；nav/header/footer/aside 是页面
// 框架不是正文；noscript/template/iframe/svg 不产生可读文本；form 控件文案会
// 打断正文。title 的文本单独提取（输出前置），正文里跳过避免重复出现。
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "iframe": true, "title": true,
	"nav": true, "header": true, "footer": true, "aside": true, "form": true,
}

// blockTags 在输出里产生换行边界：没有它们，整页正文会被拼成一行。
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ul": true, "ol": true, "table": true, "tr": true,
	"blockquote": true, "pre": true, "section": true, "article": true,
	"main": true, "dl": true, "dt": true, "dd": true,
	"figure": true, "figcaption": true, "details": true, "summary": true,
	"center": true,
}

// extractReadable 从 HTML 提取文档标题与正文文本。
// 状态机：注释/doctype 整段丢弃；skipTags 跳到对应闭标签；其余标签只留文本；
// 块级标签产生换行；行累积到块边界才落行（行内标签如 <code> 不得打断句子）。
// 已知取舍：属性值里的 ">" 会提前结束标签（罕见，且只影响噪声边界）；
// <pre> 的排版空格会被折叠——可读性优先于排版保真。
func extractReadable(doc string) (title, text string) {
	title = titleOf(doc)
	var out, run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if line := collapseSpace(decodeEntities(run.String())); line != "" {
			out.WriteString(line)
			out.WriteByte('\n')
		}
		run.Reset()
	}
	for i := 0; i < len(doc); {
		if doc[i] != '<' {
			run.WriteByte(doc[i])
			i++
			continue
		}
		rest := doc[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"): // 注释整段剥离
			i = indexAfter(rest, "-->", i, len(doc))
		case strings.HasPrefix(rest, "<!"), strings.HasPrefix(rest, "<?"): // doctype/CDATA/PI
			i = indexAfter(rest, ">", i, len(doc))
		case strings.HasPrefix(rest, "</"): // 闭标签
			if name := tagName(doc[i+2:]); name != "" && (blockTags[name] || skipTags[name]) {
				flush()
			}
			i = indexAfter(rest, ">", i, len(doc))
		default:
			name := tagName(doc[i+1:])
			if name == "" { // "<" 后不是标签（如 "a < b"）：按字面文本保留
				run.WriteByte('<')
				i++
				continue
			}
			flush() // 开标签：块级换行、噪声开始，都要求先落当前行（噪声情形内容随后被跳过）
			if skipTags[name] {
				_, i = skipElement(doc, i, name)
			} else {
				i = indexAfter(rest, ">", i, len(doc))
			}
		}
	}
	flush()
	return title, out.String()
}

// titleOf 提取 <title> 文本（大小写不敏感；缺省返回空串）。超长截断：
// 标题直接进输出且不经 64KB 预算，必须自带上限。
func titleOf(doc string) string {
	for i := 0; i < len(doc); {
		k := strings.IndexByte(doc[i:], '<')
		if k < 0 {
			return ""
		}
		i += k
		if name := tagName(doc[i+1:]); name == "title" {
			contentEnd, _ := skipElement(doc, i, "title")
			open := strings.IndexByte(doc[i:], '>')
			if open < 0 || i+open+1 > contentEnd {
				return ""
			}
			return collapseSpace(decodeEntities(doc[i+open+1 : contentEnd]))
		}
		i++
	}
	return ""
}

// skipElement 从 start（<name 处）跳过整个元素：返回内容结束位置（</name 起）与
// 元素之后的位置（闭标签 ">" 后）。闭标签找不到时内容视为到文档尾——script 按
// 规范是 raw text（首个 </script 即终点，内容里的 "</div>" 不干扰）；nav 等框架
// 标签同名嵌套极少见，提前结束只是少丢一点噪声，方向安全。
func skipElement(doc string, start int, name string) (contentEnd, next int) {
	for j := start + 1; j < len(doc); {
		k := strings.IndexByte(doc[j:], '<')
		if k < 0 {
			return len(doc), len(doc)
		}
		j += k
		after := j + 2 + len(name)
		if strings.HasPrefix(strings.ToLower(doc[j:min(after, len(doc))]), "</"+name) &&
			(after >= len(doc) || isTagBoundaryByte(doc[after])) {
			end := strings.IndexByte(doc[after:], '>')
			if end < 0 {
				return j, len(doc)
			}
			return j, after + end + 1
		}
		j++ // 同名前缀（如 </navbar> 不是 </nav>）或别的标签：继续找
	}
	return len(doc), len(doc)
}

// isTagBoundaryByte 判断 "</name" 之后是否标签边界（</navbar> 不得匹配 </nav>）。
func isTagBoundaryByte(c byte) bool {
	switch c {
	case '>', '/', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// tagName 从标签起点（"<" 或 "</" 之后）取小写标签名；不是合法标签起点返回空串。
func tagName(s string) string {
	i := 0
	if i < len(s) && isAlpha(s[i]) {
		i++
	} else {
		return ""
	}
	for i < len(s) && (isAlpha(s[i]) || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	return strings.ToLower(s[:i])
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// indexAfter 返回 sep 在 s[from:] 首次出现后的绝对下标；找不到返回 missing。
func indexAfter(s, sep string, from, missing int) int {
	k := strings.Index(s, sep)
	if k < 0 {
		return missing
	}
	return from + k + len(sep)
}

// collapseSpace 折叠空白为单空格并去首尾（HTML 排版空白对纯文本无意义，
// unicode 空白含 \u00A0 一并折叠）。
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// namedEntity 收正文里高频的一小撮命名实体；HTML5 两千多个实体表不是本工具
// 该背的，未识别的按原文保留。
var namedEntity = map[string]rune{
	"amp": '&', "lt": '<', "gt": '>', "quot": '"', "apos": '\'',
	"nbsp": ' ', // 输出是纯文本，nbsp 折成普通空格更利阅读
	"copy": '©', "reg": '®', "trade": '™',
	"mdash": '—', "ndash": '–', "hellip": '…', "middot": '·',
}

// maxEntityLen 是实体名的长度上限（&#x10FFFF; 的名字 8 字符，留足余量）：
// 超过即视为字面 "&"，不做贪婪吞并。
const maxEntityLen = 32

// decodeEntities 解码命名实体与数字实体（&#NNN; / &#xHH;）；未识别的按原文保留。
func decodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 1 || semi > maxEntityLen {
			b.WriteByte('&')
			i++
			continue
		}
		name := s[i+1 : i+semi]
		if r, ok := namedEntity[name]; ok {
			b.WriteRune(r)
			i += semi + 1
			continue
		}
		if r, ok := numericEntity(name); ok {
			b.WriteRune(r)
			i += semi + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

// numericEntity 解码 &#NNN; / &#xHH;；非法码点返回替换符（可见，不静默吞）。
func numericEntity(name string) (rune, bool) {
	if len(name) < 2 || name[0] != '#' {
		return 0, false
	}
	body := name[1:]
	base := 10
	if body[0] == 'x' || body[0] == 'X' {
		base = 16
		body = body[1:]
	}
	if body == "" {
		return 0, false
	}
	var n int
	for _, c := range []byte(body) {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case base == 16 && c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case base == 16 && c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, false
		}
		n = n*base + d
		if n > 0x10FFFF {
			return utf8.RuneError, true
		}
	}
	if n == 0 || (n >= 0xD800 && n <= 0xDFFF) { // NUL 与代理区不是可用字符
		return utf8.RuneError, true
	}
	return rune(n), true
}
