import { nextTick, ref, type Ref } from 'vue'

// 距底多少像素内仍视为"锚定在底部"（给滚动判距留余量，不要求精确到 0）
const NEAR_BOTTOM_PX = 48

/**
 * 流式滚动锚定：只有用户本就在底部时才自动跟随滚动。
 * 为什么需要：流式期间无条件滚底会把"回看历史"的用户反复拽回去（旧版缺陷）。
 */
export function useAutoScroll(el: Ref<HTMLElement | null>) {
  const anchored = ref(true)

  // 滚动时重算锚定态（配合 @scroll.passive 使用，不阻塞滚动线程）
  function onScroll() {
    const node = el.value
    if (!node) return
    anchored.value = node.scrollHeight - node.scrollTop - node.clientHeight < NEAR_BOTTOM_PX
  }

  // force=true 供"回到底部"按钮使用：无视锚定直接滚。
  // scrollTo 用可选调用：滚动是装饰性能力，宿主环境缺实现（DOM 仿真/嵌入式
  // WebView 变体）时静默降级，绝不让异常打断消息渲染链路。
  async function toBottom(force = false) {
    if (!force && !anchored.value) return
    await nextTick()
    const node = el.value
    node?.scrollTo?.({ top: node.scrollHeight })
  }

  return { anchored, onScroll, toBottom }
}
