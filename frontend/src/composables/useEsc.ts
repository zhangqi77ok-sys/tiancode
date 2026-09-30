import { onBeforeUnmount, watch, type Ref } from 'vue'

// Esc 消费栈（第 3 批）：浮层（面板/菜单/灯箱）打开时注册关闭动作，全局 Esc
// 先让最上层浮层消费——否则"按 Esc 关浮层"会顺手把正在跑的回合静默中断
//（App.vue 的全局 Esc = 中断生成，是 coding 场景最恼火的一类误伤）。
// 后进先出：最后打开的浮层先消费；没有消费者时才轮到全局中断。

const handlers: (() => void)[] = []

// registerEsc 注册一个 Esc 消费者；返回注销函数（关闭浮层时必须调用）。
export function registerEsc(fn: () => void): () => void {
  handlers.push(fn)
  return () => {
    const i = handlers.indexOf(fn)
    if (i >= 0) handlers.splice(i, 1)
  }
}

// consumeEsc 交给最上层消费者处理；返回 true 表示已消费（调用方不得再中断生成）。
export function consumeEsc(): boolean {
  const fn = handlers[handlers.length - 1]
  if (!fn) return false
  fn()
  return true
}

// useEscClose 把"浮层打开时 Esc 关闭"接到消费栈：打开注册、关闭注销、卸载兜底。
// immediate：面板常以 v-if + 初始 true 挂载（如文件详情面板），首帧必须注册。
export function useEscClose(isOpen: Ref<boolean>, close: () => void): void {
  let off: (() => void) | null = null
  watch(
    isOpen,
    (open) => {
      off?.()
      off = open ? registerEsc(close) : null
    },
    { immediate: true },
  )
  onBeforeUnmount(() => off?.())
}
