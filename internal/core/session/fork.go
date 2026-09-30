package session

import "encoding/json"

// ForkDrop 是一个分叉丢弃区间 (From, To]：To 为 fork 事件自身的序号。
type ForkDrop struct {
	From int64
	To   int64
}

// ForkDrops 返回账本里全部分叉区间（第 6 批）。
// 语义：fork 事件声明"丢弃 [FromSeq, 本事件 Seq] 区间内的事件"——**左闭**是有意的：
// 「从这条用户消息重跑」要把目标 user 消息本身也丢掉（重跑会重新落一条同文本消息，
// 否则模型会看到两条重复的用户输入）。账本只追加、旧行永不改写（用户要求
// "不要悄悄改旧行"），派生/投影方据此跳过。区间可能重叠（多次重跑），
// 消费方逐区间判断即可（数量极少）。
func (l *Ledger) ForkDrops() ([]ForkDrop, error) {
	var out []ForkDrop
	if err := l.Replay(func(ev Event) error {
		if ev.Kind() != EventFork {
			return nil
		}
		var p struct {
			FromSeq int64 `json:"from_seq"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		if p.FromSeq > 0 && p.FromSeq <= ev.Seq() {
			out = append(out, ForkDrop{From: p.FromSeq, To: ev.Seq()})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ForkDropped 报告 seq 是否落在任一丢弃区间内（[From, To]，左闭右闭）。
func ForkDropped(drops []ForkDrop, seq int64) bool {
	for _, d := range drops {
		if seq >= d.From && seq <= d.To {
			return true
		}
	}
	return false
}
