// 错误文案化统一出口：bridge/IPC 抛上来的东西可能是 Error，也可能是裸字符串/对象。
// 此前全库 40+ 处各写一遍 `String(e instanceof Error ? e.message : e)`，现收敛到这一处，
// 语义改动了才需要改一个地方。
export function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
