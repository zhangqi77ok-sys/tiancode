package tools

// 回合检查点（第 6 批）：一轮内**首次**修改某文件前的内容快照，供「撤回本轮」
// 整批恢复。为什么只在首次记录：同文件在本轮被改多次时，后面几次的"写入前"是
// 上一次改后的内容——那不是"本轮开始前"的状态，用它撤回等于没撤。
// 规模与单文件撤销同一上限（超限记 Note，绝不假装能撤）。
type RoundCheckpoint struct {
	Path      string `json:"path"`
	OldExists bool   `json:"old_exists"`
	// OldContent 是轮次开始前的全文（OldExists=false 时为空 = 本轮新建，撤回即删除）。
	OldContent string `json:"old_content,omitempty"`
	// LastSHA256 是本轮最后一次写入后的内容哈希：撤回前比对，文件被改过就拒绝
	//（与单文件「恢复写入前」同一纪律，绝不覆盖用户改动）。
	LastSHA256 string `json:"last_sha256,omitempty"`
	// Note 非空表示该文件撤不回（如写入前内容超过上限、未保存快照）。
	Note string `json:"note,omitempty"`
}

// RoundCheckpointer 是可选能力：工具支持轮次检查点收集（fstool 实现）。
// 编排层在每次 Send 前 BeginRound、流收尾时 EndRound 并把结果落账本
// （EventRoundCheckpoint），「撤回本轮」按最后的检查点整批恢复。
type RoundCheckpointer interface {
	BeginRound()
	EndRound() []RoundCheckpoint
}
