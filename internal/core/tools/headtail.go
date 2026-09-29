package tools

import (
	"fmt"
	"unicode/utf8"
)

// HeadTail 是"保留头部 + 保留尾部 + 中间标注丢了多少字节"的截断（0.0.06 用户要求）。
//
// 为什么不用"只留头部"：日志与 diff 的关键信息天然偏向尾部——测试失败的 FAIL 汇总、
// git diff 末尾的文件、命令输出的最终错误都在结尾；只留头部会让模型对着开头
// 的填充内容猜结尾。头尾配比刻意让尾部权重大于头部（2:3）。
//
// 两侧都在 UTF-8 边界回退（绝不切出非法字符，审计#8 同纪律）。
// limit 以字节计；s 不超限时原样返回（幂等友好）。
func HeadTail(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	headCap := limit * 2 / 5
	tailCap := limit - headCap
	head := utf8SafeCut(s[:headCap])
	// 尾部从后往前找 UTF-8 起点：多字节字符不能从中间开始
	tail := s[len(s)-tailCap:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	omitted := len(s) - len(head) - len(tail)
	return head + fmt.Sprintf("\n...[truncated: %d middle bytes omitted]...\n", omitted) + tail
}

// utf8SafeCut 从头截断并回退到 UTF-8 边界。
func utf8SafeCut(cut string) string {
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// HeadTailWriter 是流式场景的 HeadTail 累加器（search/shell 的逐行输出）：
// 头部缓冲写满后，后续内容进尾部环形缓冲，被挤出的字节计入 dropped。
// String() 产出与 HeadTail 同构的"头 + 中间标注 + 尾"文本。
type HeadTailWriter struct {
	headCap, tailCap int
	head             []byte
	tail             []byte // 环形
	tailStart        int
	tailLen          int
	dropped          int
	total            int
}

// NewHeadTailWriter 构造：limit 为字节总预算（与 HeadTail 同配比）。
func NewHeadTailWriter(limit int) *HeadTailWriter {
	if limit < 64 {
		limit = 64
	}
	headCap := limit * 2 / 5
	return &HeadTailWriter{headCap: headCap, tailCap: limit - headCap}
}

// Write 追加一段输出（一次一行或一块）；永不失败，超出预算的字节被安全丢弃。
func (w *HeadTailWriter) Write(p []byte) {
	w.total += len(p)
	if len(w.head) < w.headCap {
		room := w.headCap - len(w.head)
		if len(p) <= room {
			w.head = append(w.head, p...)
			return
		}
		w.head = append(w.head, p[:room]...)
		p = p[room:]
	}
	if cap(w.tail) < w.tailCap {
		w.tail = make([]byte, w.tailCap)
	}
	for len(p) > 0 {
		free := w.tailCap - w.tailLen
		if free == 0 {
			// 环满：按需驱逐最老的字节（成批，最多一圈），计入 dropped
			evict := len(p)
			if evict > w.tailCap {
				evict = w.tailCap
			}
			w.tailStart = (w.tailStart + evict) % w.tailCap
			w.tailLen -= evict
			w.dropped += evict
			free = w.tailCap - w.tailLen
		}
		end := (w.tailStart + w.tailLen) % w.tailCap
		n := w.tailCap - end // 连续可写空间（不越过切片末尾）
		if n > free {
			n = free
		}
		if n > len(p) {
			n = len(p)
		}
		copy(w.tail[end:end+n], p)
		p = p[n:]
		w.tailLen += n
	}
}

// Full 报告是否已进入"尾部滚动丢弃"状态（调用方可据此提前收束遍历以控 CPU）。
func (w *HeadTailWriter) Full() bool { return w.dropped > 0 }

// Parts 返回原始字节（头、尾）与丢弃计数：调用方需要先做字节级后处理
// （如 shell 控制台代码页解码）再拼接时使用，否则直接用 String()。
func (w *HeadTailWriter) Parts() (head, tail []byte, dropped int) {
	h := make([]byte, len(w.head))
	copy(h, w.head)
	if w.tailLen > 0 {
		if w.tailStart+w.tailLen <= w.tailCap {
			tail = append(tail, w.tail[w.tailStart:w.tailStart+w.tailLen]...)
		} else {
			tail = append(tail, w.tail[w.tailStart:]...)
			tail = append(tail, w.tail[:w.tailStart+w.tailLen-w.tailCap]...)
		}
	}
	return h, tail, w.dropped
}

// String 产出"头 + 中间标注 + 尾"。
func (w *HeadTailWriter) String() string {
	if w.dropped == 0 && w.tailLen == 0 {
		return string(w.head)
	}
	var tailOut []byte
	if w.tailLen > 0 {
		if w.tailStart+w.tailLen <= w.tailCap {
			tailOut = w.tail[w.tailStart : w.tailStart+w.tailLen]
		} else {
			tailOut = append(append([]byte{}, w.tail[w.tailStart:]...), w.tail[:w.tailStart+w.tailLen-w.tailCap]...)
		}
	}
	head := utf8SafeCut(string(w.head))
	tailStr := string(tailOut)
	for len(tailStr) > 0 && !utf8.ValidString(tailStr) {
		tailStr = tailStr[1:]
	}
	return head + fmt.Sprintf("\n...[truncated: %d middle bytes omitted]...\n", w.dropped) + tailStr
}
