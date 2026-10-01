import { computed } from 'vue'
import { useChatStore } from '../stores/chat'

// 上下文油表（0.3 驾驶位）：顶栏与 Composer 输入框**同一份读数、同一套文案**。
// 数据源只有 chat store 的 promptTokens / contextInfo（后端 chat:usage + chat:context），
// 不做第二份状态——两处显示永远一致。
//
// 折叠绝不静默：本轮折了什么（旧工具输出 / 图片 / 重复读 / 旧回复）写进完整读数，
// 折叠明细字段由后端 chat:context 逐项下发；旧后端/旧数据缺字段时按 0 处理。
export interface GaugeUsage {
  pct: number
  bar: string
  short: string
  full: string
  // foldNote 是折叠明细（"旧工具输出 2 · 重复读 1"）；没有折叠时为空串
  foldNote: string
}

export function useContextGauge() {
  const store = useChatStore()

  const usage = computed<GaugeUsage | null>(() => {
    const n = store.promptTokens
    if (!n) return null
    const tok = n >= 10000 ? `≈${(n / 1000).toFixed(1)}k tok` : `${n} tok`
    const ctx = store.contextInfo
    if (!ctx || ctx.budgetTokens <= 0) {
      // 读数缺失（正常路径下不会出现：未声明上限时后端也会给默认预算），不编造百分比
      return {
        pct: 100,
        bar: 'bg-[var(--c-text-faint)]',
        short: `${tok} · 无预算读数`,
        full: `上下文 ${n} tok · 本轮没有上下文预算读数（后端未上报）`,
        foldNote: '',
      }
    }
    const left = Math.max(0, ctx.budgetTokens - n)
    const pct = Math.round((left / ctx.budgetTokens) * 100)
    const budget = `${(ctx.budgetTokens / 1000).toFixed(0)}k tok`
    // 折完了还是超：两种情况含义不同（阶段 5-2 修订）——渠道上限是硬限制（不发请求），
    // 默认值只是折叠阈值（照发）。写成同一句会让用户以为默认预算也能拒掉他的回合。
    const fold = ctx.dropped
      ? ctx.budgetDefault
        ? ' · 已尽量折叠'
        : ' · 已达上限'
      : ctx.folded > 0
        ? ` · 已折叠 ${ctx.folded} 项`
        : ''
    // 折叠明细（0.3）：写清"折了什么"，而不是只报总数——用户需要知道丢的是
    // 工具输出还是对话正文（禁止静默丢历史的界面侧保证）。
    const parts: string[] = []
    if (ctx.foldedTools) parts.push(`旧工具输出 ${ctx.foldedTools}`)
    if (ctx.foldedImages) parts.push(`图片 ${ctx.foldedImages}`)
    if (ctx.foldedReads) parts.push(`重复读 ${ctx.foldedReads}`)
    if (ctx.foldedBodies) parts.push(`旧回复 ${ctx.foldedBodies}`)
    const foldNote = parts.join(' · ')
    // 预算来源（阶段 5-2）：渠道没声明上限时用的是保守默认值——必须写明"按默认预算"，
    // 否则用户会以为渠道里填过这个数（折叠不是静默行为）
    const fullSuffix = foldNote ? `；折叠明细：${foldNote}` : ''
    return {
      pct,
      bar: pct > 30 ? 'bg-[var(--c-primary)]' : pct > 10 ? 'bg-[var(--c-warn)]' : 'bg-[var(--c-err)]',
      short: ctx.budgetDefault ? `余 ${pct}% · 按默认预算${fold}` : `余 ${pct}%${fold}`,
      full: ctx.budgetDefault
        ? `上下文 ${n} tok / 默认预算 ${budget}（余 ${pct}%）${fold}；渠道未声明上下文上限，这是保守默认值——在「渠道管理」里填 contextLimit 可覆盖` +
          (ctx.dropped ? '（默认值只作折叠阈值、不是硬限制：本轮照发，若上游装不下会自己报错）' : '') +
          fullSuffix
        : `上下文 ${n} tok / 上限 ${budget}（余 ${pct}%）${fold}` + fullSuffix,
      foldNote,
    }
  })

  return { usage }
}
