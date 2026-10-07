// Package oemtext 把控制台字节解成 UTF-8 文本。
// 中文 Windows 的 cmd 输出常是 OEM 代码页（简中 = GBK/CP936），直接当 UTF-8
// 会得到满屏替换符。合法 UTF-8 原样通过；否则按 GBK 解码；都解不开就原样返回。
package oemtext

import (
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Decode 把控制台输出解码为 UTF-8 字符串。
func Decode(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}
