# 前端需求：审批开关修复 + 输入区重排 + 模型选择合并 + 示例问题聚焦 + 审批设置 / 首次引导 / 快捷键 / 输入历史 / 关于

> 任务 1~4 为第一批，任务 5~9 为第二批（2026-10-19 补充）；一次实施完九项。
> 注意：`frontend/src/wails.ts` 里可能已有上一次中断运行留下的 `DefaultApprovalPolicy` 两行（接口声明 + 离线桩），核对正确即可保留，不要重复添加。

日期：2026-10-19 ｜ 实施：Antigravity CLI ｜ 验收：Claude ｜ 后端配套：已完成（见"后端已就绪"）

## 分工与约束

- 只改 `frontend/` 下的文件；**不改任何 Go 代码**，不提交 git（验收通过后由 Claude 提交）。
- 遵守仓库既有纪律（`docs/STANDARDS.md`、`docs/ARCHITECTURE.md`「设计风格」）：
  - 颜色一律引用 `frontend/src/style.css` 的令牌（`var(--c-…)`），不散落硬编码色值；
  - 注释用中文、写"为什么"，密度与周围代码一致；
  - 失败必须可见（`store.error` / toast），绝不静默；
  - 浏览器调试桩（`bridge()` 无 wails 注入时）：读操作返回空数据，写操作走 `offlineWrite` 显式报错。
- 每一项都要有 vitest 回归测试；新增的桥接方法同时加进 `WailsApp` 接口与离线桩。
- 现有测试（`npm test`，当前 42 个文件 / 323 个用例）必须全部保持通过；`npx vue-tsc --noEmit` 零错误。

## 后端已就绪

| 端点 | 说明 |
| --- | --- |
| `DefaultApprovalPolicy(): Promise<string[]>` | 出厂默认审批清单（当前为 `shell, ext_manage, mcp, browser`）。单一来源在后端，前端**不得**自己写死。已有 Go 测试 `TestDefaultApprovalPolicy_MatchesFreshInstall` |

其余用到的端点均为既有：`ApprovalPolicy`、`SetApprovalPolicy`、`SetActiveModel`（经 `channels` store）、`SendWithModel`（经 `store.send(..., { model })`）。

---

## 任务 1（最高优先）：审批开关"关→开"丢失 mcp / browser 保护

### 现状（bug）

- `frontend/src/components/AppHeader.vue` 的 `toggleApproval()`：开启时调用
  `store.setApprovalPolicy(next ? ['shell', 'ext_manage'] : [])`——写死两项。
- 新装默认清单是四项（`shell, ext_manage, mcp, browser`）。用户只要把开关关掉再打开一次，
  清单就被永久保存成两项：此后 MCP 调用与浏览器操作（可提交表单）不再询问，而开关仍显示"开"。
- 同文件第 274 行、第 472 行附近的注释（"覆盖 shell 与 ext_manage"）已过时。

### 要求

1. 开启审批时，清单取自后端 `DefaultApprovalPolicy()`，原样传给 `SetApprovalPolicy`。前端源码里不得再出现写死的审批工具名数组。
2. 关闭审批仍然是 `SetApprovalPolicy([])`（行为不变）。
3. 失败处理保持"乐观翻转 + 失败回滚"：`DefaultApprovalPolicy` 或 `SetApprovalPolicy` 任一失败 → 开关回到原状态，错误经现有错误通道可见；**失败时绝不退回任何写死的清单**。
4. 开关的 `title`（悬停提示）写明当前受审批的工具，例如"需要确认：shell、ext_manage、mcp、browser"。数据来自 `ApprovalPolicy()`，开关变化后刷新。
5. 更新过时注释。
6. `wails.ts`：`WailsApp` 接口加 `DefaultApprovalPolicy(): Promise<string[] | null>`；离线桩返回 `[]`（读操作）。

### 验收标准

- [ ] vitest：模拟当前策略非空 → 点开关关闭 → `SetApprovalPolicy` 收到 `[]`；再点开启 → 收到的参数**严格等于** mock 的 `DefaultApprovalPolicy` 返回值（用一个与生产不同的清单做 mock，例如 `['shell','mcp','x']`，证明不是写死）。
- [ ] vitest：`DefaultApprovalPolicy` reject → 开关保持"关"、出现错误提示、`SetApprovalPolicy` **未被调用**。
- [ ] vitest：`SetApprovalPolicy` reject → 开关回滚。
- [ ] 静态检查：`frontend/src` 非测试文件中搜索 `'ext_manage'`，不再出现在任何数组字面量里。
- [ ] 悬停提示列出的工具与 `ApprovalPolicy()` 返回值一致。

### 已知影响（无需前端处理，写进交付说明即可）

已经踩过这个 bug 的用户，盘上清单仍是两项；前端无法区分"被 bug 改的"与"用户有意删的"。修复后用户把开关关→开一次即恢复完整四项。

---

## 任务 2：输入区改为两行布局（输入框占满宽度）

### 现状

- `frontend/src/components/Composer.vue` 的 `<div class="relative flex items-end gap-2">` 一行里依次排着：
  左侧模型药丸 → textarea → 上下文油表（有数据时）→ "跟随默认渠道"下拉 → 方案 → 附件 → 隐藏 input → 发送。
- 实测 1280×800 窗口：对话列 768px，这一行 736px，**textarea 只有 315px**，占位提示折成两行；
  有上下文油表时更窄。800px 宽窗口下更挤。

### 要求

1. 改成上下两行，整体仍是一个视觉块（同一个圆角容器）：
   - 第一行：textarea，**占满容器内宽**；`MIN_INPUT_HEIGHT_PX`/`MAX_INPUT_HEIGHT`、`autoGrow`、`resize-y` 行为不变。
   - 第二行（工具栏）：左侧 = 模型选择（任务 3 合并后的单个控件）+ 上下文油表；右侧 = 方案、附件、发送。
2. 浮层（`@` 文件引用、`/` 技能菜单、模型菜单）仍从输入区上方弹出，不被裁切、不遮挡输入光标所在行。
3. 拖放高亮（`dragOver` 的 ring）覆盖整个输入块（两行），拖放行为不变。
4. 附件列表、"放进输入框"按钮、所有快捷键（Enter 发送、Shift+Enter 换行、输入法组字时 Enter 不发送、↑↓ 菜单导航、Esc 关浮层）行为不变。
5. 不引入新的颜色或尺寸硬编码；圆角/边框沿用现有令牌（输入 `--r-input`，药丸 `--r-pill`）。

### 验收标准

- [ ] 1280×800 窗口：textarea 宽度 ≥ 输入块内宽的 95%（实测应约 700px 以上，原为 315px）；占位提示单行显示完整。
- [ ] 800×600 窗口：textarea 宽度 ≥ 输入块内宽的 95%；工具栏不换行溢出、页面无横向滚动条。
- [ ] 有上下文油表数据时，textarea 宽度不变（油表在第二行）。
- [ ] `@`、`/`、模型菜单三个浮层在 1280×800 与 800×600 下都完整可见。
- [ ] vitest：渲染 Composer，断言 textarea 与发送按钮不在同一个 flex 行容器中（或等价的结构断言）。
- [ ] vitest（新增，当前没有这条测试）：Enter 发送；Shift+Enter 不发送；`isComposing` 为真时（中文输入法选词）Enter 不发送。
- [ ] 现有 `Composer.test.ts` 全部通过。

---

## 任务 3：两个模型选择器合并为一个

### 现状

- 左侧药丸（`modelMenuOpen` / `pickModel`）：切换**默认**模型，调 `channels.setActiveModel`，改变默认渠道。
- 右侧下拉（`overrideModel`，"跟随默认渠道"）：只给发送用（`store.send(..., { model })`），提示写"本轮将用 X 发送（不改默认渠道）"。
- 两个控件并排，用户分不清该用哪个。**另有语义 bug**：`overrideModel` 发送后从不复位——提示说"本轮"，实际一直沿用到后面每一轮（含重跑）。

### 要求

1. 只保留一个模型控件（放在任务 2 的工具栏左侧）。按钮文字：
   - 无临时模型时显示默认模型名（`channels.activeModel`；未配置时"未选择模型"）；
   - 选了临时模型时显示"本轮：<模型名>"，并带一个可点的 ✕ 清除临时模型（清除不改默认）。
2. 菜单内容（一个菜单，两种操作，必须文字区分清楚）：
   - 模型列表，每行显示模型名 + 渠道名，当前默认模型标"默认"，当前临时模型标"本轮"；
   - 点模型行 = **设为默认**（沿用 `pickModel` → `setActiveModel`）；
   - 每行另有一个次要操作"仅下一条用它"（或菜单底部一个"只用于下一条消息"的勾选，二选一，以清晰为准）= 设临时模型，不改默认；
   - 没有可用模型时沿用现有引导文案（去「渠道管理」添加）。
3. **临时模型只作用于下一条成功发出的消息**：发送成功后自动清空；发送失败保留（便于重试）。重跑/重答若沿用临时模型，同样在成功后清空。
4. 删除右侧原 `<select>`。

### 验收标准

- [ ] 输入区里只有一个模型相关控件（DOM 中不再有"跟随默认渠道"下拉）。
- [ ] vitest：点模型行 → `setActiveModel` 被调用，发送时 `store.send` 的 `model` 为 `undefined`。
- [ ] vitest：选"仅下一条用它"→ 按钮显示"本轮：X" → 发送成功 → `store.send` 收到 `model: 'X'`，之后按钮恢复显示默认模型；再发送一条 → `model` 为 `undefined`。
- [ ] vitest：临时模型下发送失败（`store.send` reject）→ 仍显示"本轮：X"。
- [ ] vitest：点 ✕ → 临时模型清除，`setActiveModel` 未被调用。
- [ ] 菜单可用键盘关闭（Esc，沿用 `useEscClose`），点外部关闭。

---

## 任务 4：点击空状态示例问题后聚焦输入框

### 现状

- `MessageList.vue` 空状态的示例问题按钮 `emit('suggest', s)`，`App.vue` 处理为 `draft = $event`。
- 实测：文字填进了输入框，但焦点不在输入框（`document.activeElement` 不是 textarea），用户还得再点一下才能按 Enter。
- 按钮悬停有颜色变化，但鼠标指针是默认箭头（`cursor: default`），看不出可点。

### 要求

1. 点击示例问题后：填入草稿 → 等 DOM 更新 → 聚焦 textarea，光标放在文本末尾。直接按 Enter 即可发送。
2. 复用 `App.vue` 已有的 `composerInput` 引用（`focus-input` 快捷键用的同一个），不新建第二套聚焦途径。
3. 示例问题按钮加 `cursor-pointer`。
4. 草稿已有内容时的行为保持现状（直接替换），不在本任务改动。

### 验收标准

- [ ] vitest：触发 `suggest` 后，`document.activeElement` 是输入框，`selectionStart === selectionEnd === value.length`。
- [ ] 手工：点示例问题 → 直接按 Enter → 消息发出（调试桩下 `Send` 被调用）。
- [ ] 示例问题按钮计算样式 `cursor` 为 `pointer`。

---

## 任务 5：设置菜单新增「审批与权限」面板

### 后端已就绪

| 端点 | 说明 |
| --- | --- |
| `ApprovableTools(): Promise<string[]>` | 可放进审批清单的工具名（勾选项来源，后端单一来源） |
| `ApprovalPolicy()` / `SetApprovalPolicy(tools)` / `DefaultApprovalPolicy()` | 既有 / 任务 1 |
| `SessionShellAllow(sessionID): Promise<string[]>` | 当前会话已授予「总是允许」的 shell 前缀 |
| `RevokeSessionShellAllow(sessionID, prefix): Promise<void>` | 撤销一条（立即生效；不存在会报错） |

### 要求

1. 侧栏底部设置菜单加一项「审批与权限」，打开一个面板（复用 `BaseModal` 与现有设置面板的样式）。
2. 面板上半部分：「执行前需要确认的工具」——`ApprovableTools()` 每项一个复选框，勾选状态取 `ApprovalPolicy()`；每项一行中文说明（如 shell=执行命令、browser=操作浏览器（可提交表单）、mcp=调用 MCP 工具、ext_manage=增删扩展、fs=读写文件……，未知名字只显示原名）。按钮：「保存」→ `SetApprovalPolicy(勾选项)`；「恢复默认」→ 勾选 `DefaultApprovalPolicy()` 的结果（仍需点保存）。全部取消勾选保存 = 关闭审批，保存前用确认框提示"将关闭全部审批"。
3. 面板下半部分：「本会话已允许的命令前缀」——列出 `SessionShellAllow(当前会话)`，每条一个「撤销」按钮；空列表显示"本会话还没有授予过"；注明"重启应用后失效"。
4. 保存或撤销后，顶栏审批开关的开/关状态与悬停提示同步刷新（与任务 1 同源）。
5. `wails.ts` 接口与离线桩补齐上述三个新端点（读操作桩返回 `[]`，写操作走 `offlineWrite`）。

### 验收标准

- [ ] vitest：勾选状态与 `ApprovalPolicy()` 一致；改勾选后保存，`SetApprovalPolicy` 收到勾选项（顺序以 `ApprovableTools()` 为准）。
- [ ] vitest：「恢复默认」后保存，参数等于 `DefaultApprovalPolicy()` 的 mock 返回值。
- [ ] vitest：全部取消勾选保存时先出确认；取消确认则 `SetApprovalPolicy` 未被调用。
- [ ] vitest：撤销前缀 → `RevokeSessionShellAllow(当前会话, 前缀)` 被调用，列表刷新；失败时错误可见、列表不变。
- [ ] 保存后顶栏开关状态正确（策略为空=关，非空=开）。

---

## 任务 6：首次使用引导（没有配置模型时）

### 现状

空状态大标题是"未选择模型"，示例问题照常可点，但没有渠道时发出去必然失败。

### 要求

1. 没有任何可用模型时（`channels` store 的模型列表为空）：空状态显示一句说明"先添加一个模型渠道才能开始对话"和一个主按钮「配置模型渠道」，点击打开现有的「模型与渠道管理」面板（与设置菜单里那一项同一入口）。
2. 此时不显示示例问题；输入框可以打字，但发送按钮禁用，悬停提示"请先配置模型渠道"。
3. 配置好渠道后（模型列表非空）立即恢复正常空状态，无需刷新。

### 验收标准

- [ ] vitest：模型列表为空 → 出现「配置模型渠道」按钮、不出现示例问题、发送按钮 disabled；点击按钮打开渠道面板（断言打开事件/状态）。
- [ ] vitest：模型列表变为非空 → 示例问题出现、发送按钮可用。

---

## 任务 7：快捷键可发现

### 现状

`?` 可打开 `ShortcutsOverlay`，Ctrl+N / Ctrl+B / Ctrl+↑↓ / Ctrl+I 等都已实现，但界面上没有任何入口。

### 要求

1. 设置菜单加一项「快捷键」，右侧灰字显示 `?`，点击打开同一个 `ShortcutsOverlay`。
2. `ShortcutsOverlay` 的内容与 `frontend/src/composables/shortcuts.ts` 的实际映射一致（若有缺漏补上，如 Ctrl+B 收起侧栏）。

### 验收标准

- [ ] vitest：点击设置菜单「快捷键」→ 快捷键浮层出现；Esc 关闭。
- [ ] 浮层列出的组合键覆盖 `matchShortcut` 的全部分支（测试里逐条断言文本存在）。

---

## 任务 8：输入框历史回溯

### 要求

1. 输入框**为空**且没有任何浮层（`@`、`/`、模型菜单）打开时，按 ↑ 填入当前会话上一条用户消息原文，再按 ↑ 继续往前；↓ 往后，回到最新之后恢复为空。
2. 输入框非空、或浮层打开时，↑↓ 行为保持现状（浮层导航 / 光标移动），不触发历史。
3. 回溯出来的文本可以编辑后发送；只要用户改动过文本，历史游标就复位。
4. 只用当前会话已加载的用户消息，不新增后端调用。

### 验收标准

- [ ] vitest：会话有两条用户消息，空输入框按 ↑ 得到第二条、再 ↑ 得到第一条、↓ 回到第二条、再 ↓ 为空。
- [ ] vitest：输入框非空时按 ↑ 不改变内容；`/` 菜单打开时按 ↑ 只移动菜单选中项。

---

## 任务 9：设置菜单「关于」

### 要求

设置菜单加一项「关于 tiancode」：显示当前版本（`AppVersion()`）与「检查更新」按钮（复用 `CheckUpdate()` 与顶栏/托盘已有的更新确认流程，不另写一套）。

### 验收标准

- [ ] vitest：面板显示 mock 的版本号；点「检查更新」调用 `CheckUpdate`，无更新时显示"已是最新版本"。

---

## 任务 10：会话 token 用量显示（第三批，2026-10-19 补充）

### 后端已就绪

`SessionUsage(sessionID): Promise<SessionUsageDTO>`，字段：`promptTokens` / `completionTokens` / `totalTokens` / `cacheReadTokens` / `cacheWriteTokens` / `turns`（带用量的轮数）/ `reportedAll`（false = 有轮次上游没报用量，数字是下限）。数据来自每轮结束时落账的用量，上游不报就没有。

### 要求

1. 输入区工具栏（任务 2 的第二行）上下文油表旁边显示本会话累计 token，如"本会话 12.3k tok"；悬停提示列出输入/输出/缓存命中/缓存写入与轮数；`reportedAll=false` 时数字前加"≥"并在提示里写明"部分轮次上游未报告用量"。
2. 每轮终态后（`chat:terminal`）与切换会话时刷新；`totalTokens` 为 0 时不显示。
3. `wails.ts` 接口与离线桩补齐（桩返回全 0）。

### 验收标准

- [ ] vitest：mock 返回 12345 → 显示"12.3k"；`reportedAll=false` → 显示"≥"；全 0 → 不显示。
- [ ] vitest：触发终态事件后重新调用 `SessionUsage`。

---

## 交付与验收流程

1. Antigravity 完成后，在交付说明里逐条对照上面的复选框，写明改了哪些文件、新增了哪些测试。
2. Claude 验收：
   - 读 diff，检查只改了 `frontend/`、没有硬编码颜色、没有写死审批清单；
   - 运行 `cd frontend && npx vue-tsc --noEmit && npm test`；
   - 启动 `npx vite`，在 1280×800 与 800×600 两个尺寸下测量 textarea 宽度、检查三个浮层、走一遍示例问题→Enter；
   - 全部通过后提交；不通过则列出具体未达项退回。
