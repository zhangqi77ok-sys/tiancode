package session

// EventTodo 记录任务清单快照（agent 在模型调用 todo 工具时追加，每次全量）。
// Replay 投影只保留最新一条并原位更新（与实时 UI 的单卡语义一致）；
// 旧账本无该事件，恢复时无任务卡（向后兼容）。
const EventTodo EventKind = "todo"
