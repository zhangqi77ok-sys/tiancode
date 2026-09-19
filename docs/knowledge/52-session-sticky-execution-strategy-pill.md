# 52. 会话级粘性执行策略胶囊架构与免拦截即发交互演进

## ① 知识点与问题背景 (Context & Problem Statement)

在前期 HITL（人机协同）与策略权限工程落地中，为了防止模型在未授权情况下擅自修改工作区代码，前端在 `handleSend()` 发送入口处引入了模态拦截逻辑：每次用户输入 Prompt 并按下回车时，全屏强制弹出「架构执行策略确认」模态框，要求用户在 `只读审查 (Analyze)`、`直接改代码 (Implement)`、`TDD 闭环 (TDD)` 中做出单选决策后方可真正提交请求。

然而，在真实开发者高频使用场景下，该设计暴露了极其严重的体验缺陷与交互摩擦（UX Friction）：
1. **高频输入流被打断**：发送输入是 IDE 对话中最高频的行为。当用户询问“这行代码什么意思？”或回复“继续”时，都会被全屏居中弹窗打断，带来严重的认知负荷与操作疲劳；
2. **Shift-Left 安全伪命题**：在模型尚未推理、甚至未生成首个 Token 前，逼迫用户预判 AI 是否需要改代码，属于防线前移（Shift-Left）过当。真正的安全边界应当建立在 **Action-Time（微内核执行工具前 Rail 拦截）** 与 **Diff-Time（改动后在编辑器中展示 Monaco Diff 待用户审核采纳）**；
3. **底栏视觉与弹窗功能重合**：底栏驾驶舱已有策略指示药丸，但依然在按回车时重复弹窗，形成交互割裂。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 安全防线分层模型 (Defense-in-Depth Layering)
一个健壮的桌面 AI IDE 不应在单纯的自然语言发问阶段设置阻断式栅栏，而应建立分层治理机制：
- **Layer 1: Session-Sticky 意图与偏好策略层 (Prompt Context)**：
  通过底栏常驻胶囊（Pill）声明会话级策略，将策略注入 System Prompt（如 `[执行策略 implement: 直接改代码]`），指导大模型按既定规范思考；
- **Layer 2: JIT 微内核 Rail 拦截层 (Action-Time Enforcement)**：
  在微内核 `internal/core/loop/tools.go` 的 `runTool` 中，`DenyByStrategy` 和 `rail.OnBeforeAct()` 对具体工具调用实施零信任校验。当处于只读策略时，任何写盘工具（`fs_control` 写模式）与外部命令（`exec_command`）被微内核绝对阻断；
- **Layer 3: Diff 审查与版本回滚层 (Diff & Snapshot Recovery)**：
  当处于改码或 TDD 策略时，模型写入产生的改动由 Monaco Diff 呈现给用户逐行审阅；且在操作前自动创建轻量快照，用户可随时一键回滚。

### 2. 状态机持久化与轮转设计 (State Persistence & Cyclic Toggle)
通过将策略持久化至 `localStorage`（键名 `tiancode_execution_strategy`），实现了策略记忆与会话级粘性（Session-Sticky）。新建会话或重启应用时无需重新配置。底栏采用一体化双态胶囊：
- **主区域点击**：直接在 `implement ➔ analyze ➔ tdd` 三态间瞬间轮转，无需弹窗，伴随轻量 Toast 状态反馈；
- **辅助齿轮点击**：调出非阻断式的策略与附加约束配置弹窗，满足高级约束定制需求。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 移除发送入口的阻塞逻辑 (`frontend/src/stores/workbench.ts`)
彻底废弃 `strategyPickerArmed` 发送拦截计数器，解除 `handleSend` 前置拦截：
```typescript
// workbench.ts - handleSend 优化后
async function handleSend() {
  const prompt = inputPrompt.value.trim()
  if (!prompt || isStreaming.value) return
  
  if (pendingChoice.value || pendingConfirm.value) {
    pendingChoice.value = null
    pendingConfirm.value = null
    await wailsBridge.cancelStreaming()
    showToast('已取消当前等待项并中断旧任务。')
  }

  // 移除了 blocking modal，敲回车即刻发起流式推理
  const slash = prompt.split(/\s+/)[0]
  // ...
}
```

### 2. 会话粘性初始化与循环切换实现
```typescript
const initialStrategy = (localStorage.getItem('tiancode_execution_strategy') as any) || 'implement'
const executionStrategy = ref<'analyze' | 'implement' | 'tdd'>(
  ['analyze', 'implement', 'tdd'].includes(initialStrategy) ? initialStrategy : 'implement'
)

function setExecutionStrategy(strat: 'analyze' | 'implement' | 'tdd') {
  executionStrategy.value = strat
  try {
    localStorage.setItem('tiancode_execution_strategy', strat)
  } catch {}
}

function cycleExecutionStrategy() {
  const order: ('implement' | 'analyze' | 'tdd')[] = ['implement', 'analyze', 'tdd']
  const idx = order.indexOf(executionStrategy.value)
  const next = order[(idx + 1) % order.length]
  setExecutionStrategy(next)
  const label = next === 'analyze' ? '🛡️ 只读审查' : next === 'tdd' ? '🧪 TDD 闭环' : '⚡ 直接改代码'
  showToast(`已切换执行策略为：${label}`)
}
```

### 3. 驾驶舱胶囊一体化改造 (`frontend/src/components/ChatCockpit.vue`)
```vue
<div
  class="inline-flex items-center rounded-full border shadow-2xs shrink-0 transition-colors"
  :class="[
    s.executionStrategy === 'analyze' ? 'bg-amber-500/10 text-amber-700 border-amber-500/30' :
    s.executionStrategy === 'tdd' ? 'bg-emerald-500/10 text-emerald-700 border-emerald-500/30' :
    'bg-[#D96B27]/10 text-[#D96B27] border-[#D96B27]/30'
  ]"
>
  <button
    type="button"
    @click="s.cycleExecutionStrategy"
    class="text-xs font-semibold pl-2.5 pr-1.5 py-1 cursor-pointer outline-none hover:opacity-80 transition-opacity flex items-center gap-1"
    title="点击快速轮转切换执行策略：改代码 ➔ 只读审查 ➔ TDD 闭环"
  >
    <span>{{ s.executionStrategy === 'analyze' ? '🛡️ 只读审查' : s.executionStrategy === 'tdd' ? '🧪 TDD 闭环' : '⚡ 直接改代码' }}</span>
  </button>
  <button
    type="button"
    @click.stop="s.openStrategyPicker"
    class="pr-2 pl-0.5 py-1 text-[10px] text-black/40 hover:text-black/80 cursor-pointer outline-none transition-colors"
    title="配置策略高级附加约束与说明 (⚙️)"
  >
    ⚙️
  </button>
</div>
```

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **避免在单一高频事件上挂载模态拦截**：
   - 任何在“发送（Enter）”、“保存（Ctrl+S）”等毫秒级高频肌肉记忆操作上插入的全屏模态框，都会导致产品可用性断崖式下跌；
   - 权限管控应下沉到**真正具有副作用的 I/O 边界**（如文件写、外部进程调用），由底层 Rail 守卫实现 JIT（Just-In-Time）精细裁决。

2. **区分“配置面板”与“阻断审批”**：
   - 策略选择属于“运行模式配置”，应当平级驻留在驾驶舱控件区，支持一键切换；
   - 阻断审批仅当模型产生高危动作（如 `rm -rf`、批量写盘、越界访问）时方可在对话流中就地呼出，符合人机协同契约（HITL Contract）。

3. **策略与安全状态强一致**：
   - 前端切换为 `analyze` 后，微内核在收到附带的策略指令时，自动过滤掉非只读工具，且底线由 `DenyByStrategy` 拦截，杜绝了前端仅做视觉展示而后端失控的漏洞。
