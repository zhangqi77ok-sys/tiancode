package fstool

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// 文本编码识别（2026-10-19 体检）：此前 read 把二进制与 GBK 文件的原始字节直接
// 塞进模型上下文（实测 "1|\x00\x01\xff\xfea" / "1|\xd6\xd0\xce\xc4"）；GBK 文件上
// replace 永远零匹配，write 会把文件静默改成 UTF-8。中文 Windows 的老项目里 GBK
// 源码并不少见。
//
// 规则（确定性、不猜）：
//   - 前 sniffBytes 字节含 NUL → 二进制：拒绝按文本读写，给出说明；
//   - 合法 UTF-8 → utf8（带 BOM 的记 utf8bom，写回时保留 BOM）；
//   - 否则整段能按 GBK 严格往返（解码再编码逐字节相同、无替换符）→ gbk：
//     读时转 UTF-8 给模型、写/改时按 GBK 写回；
//   - 都不是 → 未知编码：拒绝，不冒险改写。
const sniffBytes = 8000

// 编码标记（落进撤销快照/检查点的 OldEncoding；空串 = UTF-8，与旧账本兼容）。
const (
	encUTF8    = ""
	encUTF8BOM = "utf-8-bom"
	encGBK     = "gbk"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// errBinary / errUnknownEncoding 是"不是可处理的文本"的两种业务失败。
var (
	errBinary          = errors.New("binary")
	errUnknownEncoding = errors.New("unknown encoding")
)

// detectEncoding 判定整段内容的编码。data 是文件全文（调用方已按上限控制体量）。
func detectEncoding(data []byte) (string, error) {
	head := data
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "", errBinary
	}
	if utf8.Valid(data) {
		if bytes.HasPrefix(data, utf8BOM) {
			return encUTF8BOM, nil
		}
		return encUTF8, nil
	}
	if gbkRoundTrips(data) {
		return encGBK, nil
	}
	return "", errUnknownEncoding
}

// gbkRoundTrips 报告 data 是否是严格合法的 GBK：解码无替换符、再编码逐字节相同。
func gbkRoundTrips(data []byte) bool {
	dec, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil || bytes.ContainsRune(dec, utf8.RuneError) {
		return false
	}
	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes(dec)
	return err == nil && bytes.Equal(enc, data)
}

// encodingSample 是识别文件编码时读取的样本上限：read 对大文件按行分段流式读取，
// 不为识别整份读入；1MiB 的样本对源码/文本足以判定（小于它的文件即为精确判定）。
const encodingSample = 1 << 20

// detectFileEncoding 读样本判定文件编码。样本截断处可能切开一个多字节字符：
// UTF-8 去掉尾部不完整的字符再判；GBK 依次尝试去掉 0/1 个尾字节（GBK 双字节）。
func detectFileEncoding(full string, size int64) (string, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, encodingSample)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	data := buf[:n]
	if int64(n) >= size {
		return detectEncoding(data) // 全文在样本内：精确判定
	}
	head := data
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "", errBinary
	}
	trimmed := data
	if start := lastRuneStart(data); start < len(data) && !utf8.FullRune(data[start:]) {
		trimmed = data[:start] // 样本尾部切开了一个多字节字符：去掉这半个再判
	}
	if utf8.Valid(trimmed) {
		if bytes.HasPrefix(data, utf8BOM) {
			return encUTF8BOM, nil
		}
		return encUTF8, nil
	}
	if gbkRoundTrips(data) || (len(data) > 1 && gbkRoundTrips(data[:len(data)-1])) {
		return encGBK, nil
	}
	return "", errUnknownEncoding
}

// lastRuneStart 返回最后一个 UTF-8 字符的起始下标（向前最多看 UTFMax 字节）。
func lastRuneStart(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			return i
		}
	}
	return len(b)
}

// decodeText 把文件字节按编码转成 UTF-8 文本（utf8bom 保留 BOM 字符在串首，
// 与旧行为一致：replace 的子串匹配不受影响）。
func decodeText(data []byte, enc string) (string, error) {
	if enc == encGBK {
		out, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return string(data), nil
}

// EncodeText 把 UTF-8 文本按编码写回字节（撤销/撤回恢复也走这里，保证恢复后编码不变）。
// utf8bom：文本没带 BOM 时补上（模型写全文通常不会带 BOM）。GBK 无法表示的字符显式报错。
func EncodeText(text, enc string) ([]byte, error) {
	switch enc {
	case encGBK:
		out, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			return nil, fmt.Errorf("内容含 GBK 无法表示的字符（该文件是 GBK 编码，写回会丢字）：%w", err)
		}
		return out, nil
	case encUTF8BOM:
		if !bytes.HasPrefix([]byte(text), utf8BOM) {
			return append(append([]byte{}, utf8BOM...), text...), nil
		}
		return []byte(text), nil
	default:
		return []byte(text), nil
	}
}

// encodingRefusal 把识别失败翻译成给模型的可读说明。
func encodingRefusal(path string, size int64, err error) string {
	if errors.Is(err, errBinary) {
		return fmt.Sprintf("%s 是二进制文件（%d 字节），不能按文本读取或编辑。需要了解内容请用 shell 跑对应工具（如 file / certutil -dump / 专用解析器）。", path, size)
	}
	return fmt.Sprintf("%s 不是 UTF-8 或 GBK 文本（%d 字节），无法安全地按文本处理；为避免损坏文件，已拒绝。", path, size)
}

// encodingNote 是读取时附给模型的编码说明（UTF-8 无说明，保持旧输出零变化）。
func encodingNote(enc string) string {
	switch enc {
	case encGBK:
		return "[编码：GBK，已转为 UTF-8 显示；write/replace 会按 GBK 写回]\n"
	default:
		return ""
	}
}
