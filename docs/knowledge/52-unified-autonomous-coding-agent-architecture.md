# 52. 从三态互斥到全自主统一 Coding Agent 架构演进

## ① 知识点与问题背景 (Context & Problem Statement)

在早期版本中，系统为了约束 AI 的读写与测试行为，设计了三种互斥的执行策略：
1. `analyze`（只读审查）：禁止写盘与外部命令；
2. `implement`（直接改代码）：允许读写文件并执行必要命令；
3. `tdd`（测试驱动开发）：写代码后自动级联运行测试套件。

但在实际编码实践中，用户与团队敏锐地发现：**这三个模式的划分存在根本性的范畴错误（Category Error）与心智错位**：
- **正交性冲突**：`只读 (analyze)` 与 `改代码 (implement)` 是**权限访问控制（ACL）**问题；而 `TDD` 则是**工程方法论（Methodology）**。把“少油少盐”和“吃肉吃素”并列为互斥单选，导致逻辑割裂；
- **心智负担与选择困境**：选了“改代码”就不跑测试了吗？没测试的项目选 TDD 怎么算？选 TDD 会不会擅自生成不需要的测试？用户每次发问都要判断自己处于哪个模式；
- **割裂了 Agent 的自主性**：真正优秀的 AI 编程工具（如 Cursor Composer、Claude Code）都是**单一全自主 Agent**，由自然语言意图驱动自适应行为，依靠后置 Diff 与前置 Rail 安全底座保障安全。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 全自主统一 Coding Agent 架构哲学
编程智能体的核心使命是协助开发者完成工程任务，不应该在表层 UI 强加不必要的模式选择器。系统将整体架构全面收敛为**单一统一 Coding Agent（Unified Autonomous Coding Agent）**：
- **自然语言意图自适应（Intent-Adaptive Driving）**：
  - 用户要求解释原理、排查报错、审查代码 ➔ Agent 自主调用检索与只读工具，给出分析结果，绝不产生写盘副作用；
  - 用户要求新增接口、重构逻辑、修复 Bug ➔ Agent 精准定位目标代码并执行改写，自动带出 Monaco Diff 待用户审核；
  - 用户要求测试驱动或回归验证 ➔ Agent 自主补齐测试并调用真实测试套件验证。
- **三层递进式安全底座（Three-Tier Safety Substrate）**：
  1. **动作前（Action-Time Pre-Act）**：SafetyRail 实行零信任拦截，危险命令（如 `rm -rf`）直接熔断或触发人机一次授权（HITL WP-H2）；
  2. **动作时（Write-Time Snapshot）**：每次写盘前 $\le 5\text{ms}$ 自动生成轻量 Git 影子快照，支持秒级无损撤销；
  3. **动作后（Diff-Time Monaco Review）**：改动完全透明，用户在右侧 Monaco Diff 视窗中逐行审阅、细粒度 Cherry-Pick 单块采纳/丢弃或全局全部采纳，牢牢握住发货决定权。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 彻底纯化前端状态层 (`frontend/src/stores/workbench.ts`)
彻底移除 `executionStrategy`、`executionStrategies`、`isStrategyPickerOpen` 等所有模式状态及方法，Prompt 发送恢复纯净自然语言：
```typescript
// workbench.ts - handleSend 纯净发送
async function handleSend() {
  const prompt = inputPrompt.value.trim()
  if (!prompt || isStreaming.value) return

  // 保证 Diff 未确认时拦截确认
  if (pendingDiffFiles.value.length > 0 && !forceSendWithPendingDiff.value) {
    isPendingDiffPromptOpen.value = true
    return
  }

  // 纯净装配 Prompt，不附加生硬机械前缀
  let fullPrompt = prompt
  if (attachedFiles.value.length > 0) {
    fullPrompt = `[附加关联文件]\n${attachedFiles.value.map(f => `@${f}`).join('\n')}\n\n${fullPrompt}`
    attachedFiles.value = []
  }

  // 直接调用微内核 sendMessage，策略固定为统一全自主 implement
  await wailsBridge.sendMessage({
    session_id: currentSessionId.value,
    prompt: fullPrompt,
    model: selectedModel.value,
    is_full_auto: false,
    strategy: 'implement',
    strategy_note: ''
  }, /* callbacks */)
}
```

### 2. 驾驶舱界面极致收敛 (`frontend/src/components/ChatCockpit.vue`)
移除底栏的所有策略胶囊与齿轮图标，底部操作区仅保留最纯粹的输入辅助：
```vue
<div class="flex items-center justify-between border-t border-black/[0.04] pt-2 text-xs">
  <div class="flex items-center gap-1.5 min-w-0 flex-1">
    <button @click="s.triggerUpload" class="px-2.5 py-1 rounded-full text-xs text-[#52525B] hover:text-[#18181B] hover:bg-black/[0.04] flex items-center gap-1 cursor-pointer shrink-0" title="调起系统文件选择框">
      <span>📎</span><span>上传</span>
    </button>
    <div class="h-3.5 w-px bg-black/[0.1] mx-0.5 shrink-0"></div>
    <span class="text-[11px] text-[#71717A] truncate font-sans hidden sm:inline-block">
      💡 输入 @ 关联文件，/ 查看指令，自然语言自由对话与改码
    </span>
  </div>
  <div class="flex items-center gap-2 shrink-0">
    <span class="text-[10px] text-[#A1A1AA] font-mono">{{ s.isStreaming ? '正在流式推理...' : '就绪' }}</span>
    <!-- 发送 / 中断 按钮 -->
  </div>
</div>
```

### 3. 微内核系统提示词规范强化 (`internal/core/loop/strategy.go`)
为统一 Coding Agent 注入意图自适应行动准则与防盲改铁律：
```go
default:
	system += "\n[全自主统一 Coding Agent] 具备读取检索、代码编写、终端运行与测试验证的完整能力。意图自适应原则：\n 1. 当用户仅要求解释、答疑、代码审查或架构分析时，通过 search_workspace 与 read_file 只读分析并给出详尽解答，不修改工作区文件；\n 2. 当用户要求修复 Bug、实现功能、新增接口或重构代码时，先定位后精准改写，修改后自动产生 Monaco Diff 待用户审核；\n 3. 当用户要求测试驱动或验证质量时，自主调用测试算子进行验证并修复；\n 4. 探索代码时遵循「先检索/看地图再精准下钻」原则，严禁盲目全库递归遍历。"
```

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **避免在核心生产力工具中人为引入“模式地狱”**：
   - 如果一个工具每次发问都要让用户选择模式，本质上是模型意图理解与系统架构能力不足的遮羞布；
   - 依靠强大的 System Prompt 契约与工具权限体系，大模型完全具备根据上下文判断“何时只读、何时编写、何时运行测试”的自适应能力。

2. **区分“安全防线”与“操作门槛”**：
   - 真正的安全感来自于：**改动看得见（Monaco Diff）、快照回得去（Git Stash/Snapshot）、高危拦得住（SafetyRail）**；
   - 而不是在发问前用弹窗逼迫用户签“免责声明”。
