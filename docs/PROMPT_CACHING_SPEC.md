# 湉码 / tiancode 确定性前缀冻结与 KV Cache / Prompt Caching 技术架构规约

> **文档标识**：`DOCS-SPEC-202609-PROMPT-CACHING`  
> **状态**：`Draft / Standard Contract`  
> **适用版本**：`v0.0.1+`  
> **对齐架构**：Wails v2 + Go 微内核 + Vue 3 / Pinia  
> **参考标准**：Anthropic Prompt Caching API、DeepSeek Context Caching、OpenAI Prompt Caching、Google Gemini Context Caching、Claude Code 与 Cursor 缓存架构工程哲学

---

## 目录
- [一、背景、痛点与核心目标 (Executive Summary & Problem Statement)](#一背景痛点与核心目标-executive-summary--problem-statement)
- [二、主流模型厂商 Prompt Caching 底层原理与技术规约 (State-of-the-Art Industry Matrix)](#二主流模型厂商-prompt-caching-底层原理与技术规约-state-of-the-art-industry-matrix)
- [三、湉码 7 层确定性前缀流水线架构 (The 7-Layer Immutable Prefix Pipeline)](#三湉码-7-层确定性前缀流水线架构-the-7-layer-immutable-prefix-pipeline)
- [四、上下文治理：锚定修剪 vs 朴素滑动窗口 (Anchored Compaction Strategy)](#四上下文治理锚定修剪-vs-朴素滑动窗口-anchored-compaction-strategy)
- [五、Canonical JSON 序列化与字节级防抖 (Deterministic Byte-Level Serialization)](#五canonical-json-序列化与字节级防抖-deterministic-byte-level-serialization)
- [六、微内核与插件层改造成本与工程契约 (Microkernel & Provider Refactor Contract)](#六微内核与插件层改造成本与工程契约-microkernel--provider-refactor-contract)
- [七、遥测核算与前端视觉大盘 (Telemetry & UI Observability)](#七遥测核算与前端视觉大盘-telemetry--ui-observability)
- [八、分阶段实施里程碑与验收标准 (Milestones & Acceptance Criteria)](#八分阶段实施里程碑与验收标准-milestones--acceptance-criteria)

---

## 一、背景、痛点与核心目标 (Executive Summary & Problem Statement)

### 1.1 大模型推理底层的经济学与时延物理法则
在 Transformer 架构中，自回归推理分为两个截然不同的计算阶段：
1. **Prefill（首字预填充）阶段**：模型必须对输入的所有 Prompt Tokens 进行全量并行注意力计算（Full Self-Attention，计算复杂度 $O(N^2)$），生成并存储所有层、所有注意力头的键值矩阵（Key-Value Tensors，即 **KV Cache**）；
2. **Decode（逐字解码）阶段**：模型在已有 KV Cache 的基础上，每次仅计算新生成的单个 Token 与此前所有历史的注意力，自回归生成下一个 Token。

在典型的 Coding Agent 长任务场景中：
- 单轮会话上下文规模往往迅速膨胀至 **30k ~ 120k Tokens**（包含大量系统规则、数十个复杂工具的 JSON Schema 定义、项目 AST 架构、长文件代码与终端测试输出）；
- 如果每次交互都进行全量 Prefill 计算，会导致两大灾难性后果：
  - **延迟极高（Time-to-First-Token, TTFT 达 5s ~ 15s+）**，用户体验极其迟钝；
  - **费用剧烈消耗**：每推进一轮仅需生成几十个 Token 的工具调用，却必须为 50,000+ 的输入 Token 重复支付全额计费。

现代头部模型厂商（Anthropic, DeepSeek, OpenAI, Google）均引入了服务端的 **KV Cache 复用机制（Prompt Caching）**：当后续请求与此前请求存在**绝对一致的公共前缀**时，云端可直接跳过 Prefill 计算，直接从内存/高速 SSD 读取已有的 KV Cache，从而带来 **80% ~ 90% 的计费折扣** 与 **5x ~ 10x 的 TTFT 首字加速**。

---

### 1.2 审查现状：湉码 现有实现中的 4 大“缓存杀手”硬伤
经对当前代码库深入审查，系统在 Prompt 组织与模型请求流水线上存在严重的“缓存防腐失效”，导致上游缓存命中率几乎为 0：

1. **硬伤 1：滑动窗口从头部暴力削减，亲手撕碎 KV Cache**
   - [`app.go:buildConversationWindow`](file:///c:/Users/13605/tiancode/app.go) 采用从最新消息向后逆向收集、超预算直接丢弃旧轮次的朴素做法；
   - 一旦历史消息超出 32,000 字符，最前端的第 1 轮、第 2 轮消息就被抹去。在 Transformer 中，**第 0 个 Token 发生变化，后面的整个 KV 序列哈希全部失效**，云端缓存命中率归零。
2. **硬伤 2：易变脏数据穿插在静态前缀中，阻断哈希对齐**
   - [`app_chat.go`](file:///c:/Users/13605/tiancode/app_chat.go) 中将每轮都在变的 `continuationCtx`（任务接续目标、未完成清单）和容易抖动的环境探测动态追加在 `systemPrompt` 尾部；
   - 甚至在消息实体中直接注入高频秒级变化的时间戳 `Time: time.Now().Format("15:04")`，导致每一轮请求的前缀字符串 Hash 均发生漂移。
3. **硬伤 3：Go Map 迭代随机性破坏字节级一致性**
   - 工具参数定义以 `map[string]any` 传递；Go 语言中 Map 的 `range` 遍历具备故意引入的随机哈希种子；
   - 每次调用 `json.Marshal(toolDefs)` 时，字段的 Key-Value 序列顺序可能完全不同，导致 JSON 字节流不一致，上游字节级缓存判定失效。
4. **硬伤 4：缺乏厂商协议控制与遥测空白**
   - [`plugins/provider/anthropic/`](file:///c:/Users/13605/tiancode/plugins/provider/anthropic/) 中未注入 `cache_control: {"type": "ephemeral"}` 检查点；
   - [`internal/telemetry/tracker.go`](file:///c:/Users/13605/tiancode/internal/telemetry/tracker.go) 未解析或记录 `cached_tokens`，无法验证真实节省效益。

---

### 1.3 核心目标
建立工业级、确定性的前缀冻结与 Prompt Caching 治理体系，达成：
1. **缓存命中率跨越**：在多轮 Coding 会话中，系统静态前缀与历史交互的 **KV Cache 命中率稳定维持在 85% ~ 95%**；
2. **降本增效**：使多轮交互的综合 API Token 开销下降 **70% ~ 85%**，首字延迟降低至 **1.5s 以内**；
3. **协议全兼容**：同时对齐 Anthropic 显式检查点、DeepSeek 64-token 块自动对齐、OpenAI 1024-token 前缀自动对齐与 Gemini Context Caching；
4. **纯净真实遥测**：杜绝任何 Mock 假数据，100% 捕获真实上游 `usage` 中的缓存指标，在桌面端准确呈现省下的金额与 Token。

---

## 二、主流模型厂商 Prompt Caching 底层原理与技术规约 (State-of-the-Art Industry Matrix)

| 厂商 / 平台 | 缓存激活机制 | 最小缓存门槛 | 增量粒度 | 缓存有效寿命 (TTL) | 计费折扣力度 | 响应体 Usage 标识字段 |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Anthropic Claude** (3.5 Sonnet / 3.7 / Opus) | **显式断点声明**：在 System 块、Tool 定义或 Message 块携带 `cache_control: {"type": "ephemeral"}` | 1,024 tokens (Sonnet/Opus)<br>2,048 tokens (Haiku) | 任意断点位置（单请求上限 4 个断点） | 5 分钟滑动窗口（命中自动续期） | 写入：1.25x 基础价<br>**读取：0.10x 基础价 (省 90%)** | `usage.cache_read_input_tokens`<br>`usage.cache_creation_input_tokens` |
| **DeepSeek** (V3 / R1) | **全自动前缀匹配**：无需代码声明，服务端自动对从第 0 个 Token 开始的最长公共前缀进行内存/NVMe SSD 查找 | 64 tokens | 64-token chunks | 自动随访问热度淘汰 | 命中：0.14元 / 1M<br>未命中：1.0元 / 1M<br>**(省 86%)** | `usage.prompt_cache_hit_tokens`<br>`usage.prompt_cache_miss_tokens` |
| **OpenAI** (GPT-4o, o1, o3-mini) | **全自动前缀匹配**：自动匹配连续公共前缀 | 1,024 tokens | 128-token chunks | 5 ~ 10 分钟未命中淘汰 | **读取：0.50x 基础价 (省 50%)** | `usage.prompt_tokens_details.cached_tokens` |
| **Google Gemini** (1.5 Pro / Flash) | **隐式前缀缓存 + 显式 API**：超大上下文支持 `CachedContent` 资源显式创建 | 32,768 tokens (显式 API) | 显式整块生命周期 | 显式指定（默认 1 小时，按时间收取微量存储费） | **读取：0.25x 基础价 (省 75%)** | `usageMetadata.cachedContentTokenCount` |

### 行业共性核心铁律：
1. **Byte-Level Strict Prefix Match**：缓存命中必须是自 Prompt 序列第 0 个 Token 起的**完全连续公共前缀**。前缀中途一旦插入 1 个不同 Token，其后所有内容的缓存判定立即归零；
2. **Tools Must Be Frozen**：大部分 LLM 内部将 Tools 定义注入到 System Prompt 之后或专属隐藏 Prefix 中。工具声明的位置顺序变动、增删参数，会导致整个工具列表及后续所有对话无法复用缓存。

---

## 三、湉码 7 层确定性前缀流水线架构 (The 7-Layer Immutable Prefix Pipeline)

为了在多轮 Agent 执行回路中最大化命中 KV Cache，必须将发送给大模型的 Token 序列解构为严格的 **7 层时序流水线**：

```text
┌───────────────────────────────────────────────────────────────────────────────────────┐
│                               7 层确定性 Token 排布流水线                               │
├───────────────────────────────────────────────────────────────────────────────────────┤
│ [Layer 0: Core Identity & Behavioral Rules]   │ 绝对不可变纯静态文本                  │
│ [Layer 1: Project Constitution (Rules/Skills)]│ 字母字典序排序，内容哈希冻结           │ ── 永冻静态前缀
│ [Layer 2: Canonical Tool Schemas]             │ 严格键排序 JSON Schema，不可变工具池    │   (Immutable Prefix)
│ [Layer 3: Workspace AST Architecture Map]     │ 仅在依赖/文件变化时更新的架构骨架       │   [命中率 95%+]
├───────────────────────────────────────────────────────────────────────────────────────┤
│ [Layer 4: Linear Multi-Turn History]          │ 严格单向追加历史会话 (Turn 1..N-1)      │ ── 历史递增缓存
│ [Layer 5: Anchored Tool Output Pruned Blocks] │ 历史冗长工具执行输出行数截断占位符      │   (Growing Prefix)
├───────────────────────────────────────────────────────────────────────────────────────┤
│ [Layer 6: Dynamic Ephemeral Tail]             │ 任务接续上下文 + 当前技术栈 + 运行时错误 │ ── 动态尾部
│                                               │ 严格置于最末尾最新 User Prompt 之后   │   (Never In Prefix!)
└───────────────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 详细层级职责与边界准则

#### Layer 0: 核心角色与行为基座 (System Identity)
* **内容**：湉码 核心行为基座、ReAct 循环工作规约、代码修改防腐要求。
* **规则**：100% 纯硬编码静态常量，严禁插值任何动态变量。

#### Layer 1: 项目宪法规范 (Project Constitution)
* **内容**：`AGENTS.md` 铁律、`.agents/rules/` 规则集、`.agents/skills/` 技能集。
* **规则**：所有文件必须按文件路径与 ID 执行**严格的字母升序排序（Alphabetical Sort）**后再行拼接；当规则未被修改时，其 Token 序列哈希绝对恒定。

#### Layer 2: 规范化工具 Schema (Canonical Tool Schemas)
* **内容**：`v1.ToolDefinition` 声明的 Function Calling 参数 Schema。
* **规则**：
  1. 工具列表按 `tool.Name` 升序排列；
  2. JSON Schema 的所有 Key 必须使用 Canonical 排序序列化（见第五节）；
  3. 对于 Anthropic Claude，在此层末尾设置 **Cache Breakpoint 1**。

#### Layer 3: 工作区架构与依赖骨架 (Workspace Architecture Map)
* **内容**：当前项目的分层依赖 DAG 摘要（Layer、Package、Contract 核心接口），即 AST 治理工作板沉淀的骨架摘要。
* **规则**：基于工作区 AST 指纹（AST Hash）缓存，仅在真实文件新增/删除/依赖变动时重新计算，否则保持纯静态。

#### Layer 4 & 5: 线性历史轮次与修剪块 (Linear History & Pruned Blocks)
* **内容**：Turn 1 至 Turn $N-1$ 的 `user`、`assistant`（含思考链与工具调用）、`tool`（工具执行输出）消息链。
* **规则**：严格保持 Append-Only（单向只增不删）。严禁从顶部削减旧轮次！

#### Layer 6: 动态瞬态尾部 (Dynamic Ephemeral Tail)
* **内容**：
  1. 当前任务目标接续信息（`continuationCtx`）；
  2. 运行时动态技术栈探测结果（`stackPrompt`）；
  3. 前序未确认的代码 Diff 清单（`PendingDiffFiles`）；
  4. 当前轮次用户指令与 `@` 提及的文件内容。
* **物理防线铁律**：**上述动态内容绝对禁止注入 System Prompt！** 必须且只能作为最后一条 `user` 消息的末尾附件块打包（`<dynamic_context>...</dynamic_context>`）。

---

## 四、上下文治理：锚定修剪 vs 朴素滑动窗口 (Anchored Compaction Strategy)

### 4.1 传统滑动窗口为何是“缓存屠夫”
假设历史会话有 10 轮。当超出限制时：
- **朴素滑动窗口**：丢弃 Turn 1，保留 Turn 2 ~ 10。
  - Token 序列变为：`[System] + [Turn 2] + [Turn 3] ...`
  - 对云端注意力引擎而言，原先 `[Turn 1]` 对应的内存 KV Cache 彻底错位，**后方所有 Turn 2~10 的缓存全部报废**，发生全量 Cache Miss。

### 4.2 湉码 锚定前缀修剪算法 (Anchored Compaction)
为了兼顾“防止超大上下文撑爆 Token 预算”与“保持 KV Cache 最大化复用”，系统采用**两级微创修剪策略**：

```text
策略 A: 工具输出原位瘦身 (In-Place Tool Output Pruning)
──────────────────────────────────────────────────────────────────────
原始历史:
  [Tool 2: tool.search] ➔ 返回 8,000 字符的 grep 文本行 (消耗大量 Token)
修剪后:
  [Tool 2: tool.search] ➔ 保持 Message 结构与 tool_call_id 不变，原位替换为:
  "[Output pruned: 120 lines matching 'SearchPattern'. Core results cached in context.]"
  效益：仅修改特定老旧叶子节点，保持前序公共链路骨架连续，最大化收敛上下文膨胀。

策略 B: 锚定检查点折叠压缩 (Checkpoint Summarization)
──────────────────────────────────────────────────────────────────────
当全量上下文真正触达窗口水位警戒线（如 48k Tokens）：
1. 保持 [Layer 0~3 静态前缀] 绝对不动 (Checkpoint 1)；
2. 启动异步微内核总结任务，将 Turn 1 ~ K 的对话提炼为结构化的 <milestone_summary>；
3. 将 Turn 1 ~ K 替换为单一的里程碑总结消息，并在其末尾设立新的 Checkpoint 2；
4. 仅滑动 Turn K+1 之后的最新轮次。
```

---

## 五、Canonical JSON 序列化与字节级防抖 (Deterministic Byte-Level Serialization)

在 Go 语言中，标准库 `json.Marshal` 处理 `map[string]any` 时，其 Key 的遍历顺序是不确定的。如果两次请求中同一工具参数的 JSON 字符串不同，会导致大模型将 Tools 判定为已修改，击穿缓存。

### 5.1 规范化 JSON 序列化器规范
微内核必须引入强制字典序排序的规范化序列化工具：

```go
// pkg/protocol/canonical_json.go

// MarshalCanonical 将任意数据结构按 Key 字典序严格稳定排序输出 JSON 字节流
func MarshalCanonical(v any) ([]byte, error) {
    // 递归遍历 map 并对 key 执行 sort.Strings
    // 消除任何因运行时哈希随机化带来的字节漂移
}
```

### 5.2 空白字符与行尾标准化 (CRLF vs LF)
- 序列化生成的所有 System Prompt、技能与规则文本，一律使用统一的 `\n`（LF）作为换行符，严禁混用 Windows `\r\n`；
- 所有段落间空行统一压缩为单一标准空行（Trim 空白尾缀），确保在不同操作系统环境下，同一个工作区计算出的 Token 序列完全一致。

---

## 六、微内核与插件层改造成本与工程契约 (Microkernel & Provider Refactor Contract)

### 6.1 领域模型扩展 (`pkg/plugin/v1/types.go` & `internal/llm/client.go`)

在数据传输层补充 Prompt Caching 显式控制与用量捕获字段：

```go
// pkg/plugin/v1/types.go

type CacheControl struct {
    Type string `json:"type"` // "ephemeral"
}

type ChatMessage struct {
    Role         string        `json:"role"`
    Content      any           `json:"content"` // string 或 []ContentBlock
    CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type UsageInfo struct {
    PromptTokens      int   `json:"prompt_tokens"`
    CompletionTokens  int   `json:"completion_tokens"`
    TotalTokens       int   `json:"total_tokens"`
    // 工业级 Prompt Caching 真实遥测字段
    CachedTokens      int   `json:"cached_tokens"`       // 通用缓存命中数
    CacheReadTokens   int   `json:"cache_read_tokens"`   // 读缓存命中 (Anthropic / DeepSeek)
    CacheCreateTokens int   `json:"cache_create_tokens"` // 写缓存创建
}
```

### 6.2 厂商 Provider 适配契约

#### 1. Anthropic 驱动 (`plugins/provider/anthropic/`)
* **Beta Header 注入**：
  必须携带请求头：`anthropic-beta: prompt-caching-2024-07-31`
* **断点布置原则（最多 4 处）**：
  * **Breakpoint 1**：System 提示词块末尾；
  * **Breakpoint 2**：`tools` 数组中最后一个工具定义对象上；
  * **Breakpoint 3**：历史消息链中倒数第 2 轮的 `user` 或 `tool_result` 消息上；
* **Usage 提取**：
  从流式响应的 `message_start` 或 `message_delta` 的 `usage` 块提取 `cache_creation_input_tokens` 与 `cache_read_input_tokens`。

#### 2. DeepSeek / OpenAI 驱动 (`plugins/provider/openai/`)
* **零 Header，全靠前缀对齐**：
  无需额外控制字段，严格保证 `[Layer 0~3]` 超过 1024 Tokens（OpenAI）或 64 Tokens（DeepSeek）且一字不变；
* **Usage 提取**：
  在流式输出结束时的 `usage` 对象中提取：
  - OpenAI / DeepSeek: `prompt_tokens_details.cached_tokens`；
  - DeepSeek: `prompt_cache_hit_tokens`。

---

## 七、遥测核算与前端视觉大盘 (Telemetry & UI Observability)

### 7.1 真实成本核算公式 (Zero Demo Policy)
摒弃任何静态或假随机百分比，指标完全由物理请求返回的 `UsageInfo` 累加计算：

$$\text{Cache Hit Rate} = \frac{\sum \text{CacheReadTokens}}{\sum \text{PromptTokens}} \times 100\%$$

$$\text{Saved Cost (\$)} = \sum \left( \text{CacheReadTokens} \times (\text{Price}_{\text{Base}} - \text{Price}_{\text{CacheRead}}) \right)$$

### 7.2 遥测状态中心扩展 (`internal/telemetry/tracker.go`)
```go
type ModelUsage struct {
    Model             string  `json:"model"`
    Calls             int     `json:"calls"`
    TotalTokens       int     `json:"total_tokens"`
    PromptTokens      int     `json:"prompt_tokens"`
    CompTokens        int     `json:"comp_tokens"`
    CachedTokens      int     `json:"cached_tokens"`       // 真实缓存命中 Token 计数
    CacheSavingsUSD   float64 `json:"cache_savings_usd"`   // 真实节省美元金额
    CacheHitRate      float64 `json:"cache_hit_rate"`      // 命中率百分比 (0.00 ~ 100.00)
    AvgLatencyMs      int64   `json:"avg_latency_ms"`
}
```

### 7.3 前端工作台交互对齐
在顶栏状态区与对话会话结束卡片中，展示真实的缓存红利：
- 顶栏状态指示胶囊：`⚡ KV Cache 89.2% (已省 $2.14)`；
- 鼠标悬停 Tooltip 呈现明细：
  - 本会话累计 Prompt Tokens: 124,500
  - 命中 KV Cache 复用: 111,050
  - 产生 Prefill 重新计算: 13,450
  - 加速收益: 平均首字延迟降低 68% (由 4.2s 缩短至 1.3s)。

---

## 八、分阶段实施里程碑与验收标准 (Milestones & Acceptance Criteria)

### 里程碑规划 (Roadmap)

| 阶段编号 | 阶段名称 | 交付工作包 | 关联文档与测试 |
| :--- | :--- | :--- | :--- |
| **Phase 1** | **前缀流水线规范化与防抖重构** | 1. 实现 `CanonicalJSON` 字典序序列化器<br>2. 重构 `SendMessage` 上下文组装：动态上下文物理剥离出 System，沉底至最新 User 尾部<br>3. 严格冻结 Layer 0~3（System, Rules, Skills, Sorted Tools） | `internal/core/loop/canonical_test.go`<br>`internal/core/loop/prefix_test.go` |
| **Phase 2** | **Provider 协议层 Prompt Caching 注入** | 1. Anthropic 驱动注入 `cache_control` 与 `anthropic-beta` Header<br>2. DeepSeek / OpenAI 确保前缀满足最小 Token 长度与对齐阈值<br>3. 流式回调解析 `cache_read_input_tokens` / `cached_tokens` | `plugins/provider/anthropic/cache_test.go`<br>`plugins/provider/openai/cache_test.go` |
| **Phase 3** | **锚定修剪器与上下文防爆策略** | 1. 废除逆向滑动窗口，实现原位工具输出瘦身（Tool Output Pruning）<br>2. 会话断点记忆保存，保证历史连续公共链条不断裂 | `internal/core/loop/compaction_test.go` |
| **Phase 4** | **遥测大盘真实落地与端到端闭环** | 1. `telemetry.Tracker` 真实统计已节省金额与缓存命中率<br>2. 前端界面展示实时缓存收益指示灯，杜绝任何假数据 | `e2e_prompt_caching_test.go` |

### 验收交付标准 (Acceptance Criteria)
1. **自动化测试断言**：在连续 5 轮单测模拟对话中，除最后一轮 User 输入外，发送至 Provider 的前缀字节流 SHA-256 哈希值 **100% 保持一致**；
2. **Anthropic 协议验证**：抓包或 Mock 测试确认发出的请求 Payload 中：
   - System 块携带 `cache_control: {"type": "ephemeral"}`；
   - Tools 列表最后一项携带 `cache_control: {"type": "ephemeral"}`；
   - 响应端成功捕获 `cache_read_input_tokens > 0`；
3. **DeepSeek / OpenAI 验证**：在真实 API 调用下，第 2 轮起的 `cached_tokens` 或 `prompt_cache_hit_tokens` 占比稳定 $\ge 80\%$；
4. **守卫合规**：通过 `go run ./tools/archcheck` 与 `npm run build`，100% 遵守 `AGENTS.md` 铁律。

---

*本规约作为后续施工唯一技术依据。未经架构评审，严禁在业务逻辑中重新引入前缀动态插值或头部滑动削减。*
