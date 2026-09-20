# 55. 确定性前缀冻结、KV Cache / Prompt Caching 与真实遥测闭环 (Prompt Caching, KV Cache Prefix Freezing & Real Telemetry)

> **知识库编号**：`DOC-KNOWLEDGE-055`  
> **归档日期**：2026-09-20  
> **分类领域**：大模型工程 / 推理成本优化 / 确定性序列化 / 遥测核算 / UI人机工程学  
> **关联模块**：`pkg/protocol/`、`plugins/provider/`、`internal/telemetry/`、`internal/core/loop/`、`app_chat.go`、`frontend/src/components/ChatCockpit.vue`

---

## ① 知识点与问题背景 (Context & Problem Statement)

在基于 Transformer 架构的代码智能体（Coding Agent）日常开发中，多轮会话往往包含海量工程上下文：系统核心宪法规约、数十个插件算子的 JSON Schema 声明、AST 架构骨架、大型源码文件与终端构建日志，单轮上下文迅速膨胀至 30k ~ 120k Tokens。

如果不采用大模型服务端的 **KV Cache 复用机制（Prompt Caching）**，每一次大模型调用都需要对全量 Prompt 重新执行首字预填充（Prefill 计算，计算复杂度 $O(N^2)$），导致：
1. **首字延迟极高（TTFT 达 5s ~ 15s+）**，用户界面极其迟钝；
2. **Token 账单剧烈损耗**：每一轮只生成几十个 Token 的工具调用，却必须为 50k+ 的前置 Token 反复支付全额费用。

经全景代码审查发现，原系统在 Prompt 排布与模型驱动层存在 4 大致命的“缓存杀手”缺陷：
1. **滑动窗口头部截断**：`buildConversationWindow` 采用逆向收集，超出预算直接砍掉第 1、2 轮历史。在 Transformer 注意力机制中，第 0 个 Token 发生变化，后续所有 KV Cache 全部报废；
2. **动态上下文混入系统前缀**：`app_chat.go` 将每轮变动的接续任务目标 `continuationCtx` 与技术栈探测直接拼在 `systemPrompt` 中，导致每一轮的公共前缀哈希都在漂移；
3. **Go Map 随机性破坏字节级一致性**：Go 语言 `map[string]any` 迭代顺序随机，造成工具定义参数 JSON 字符串在不同轮次乱序，无法对齐上游缓存判定；
4. **厂商协议空白与遥测缺失**：Anthropic 驱动未携带缓存 Header 与断点；OpenAI 流式请求未开启 `stream_options.include_usage`；遥测中心未记录真实命中的缓存数量，无法衡量真实节省效益。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 2.1 主流模型厂商 Prompt Caching 物理机理对照
- **Anthropic Claude (3.5 Sonnet / 3.7 / Opus)**：
  - 显式声明断点机制：在请求头携带 `anthropic-beta: prompt-caching-2024-07-31`，并在 tools 或 messages 携带 `cache_control: {"type": "ephemeral"}`；
  - 最小缓存门槛为 1,024 Tokens，单次请求上限 4 个断点；
  - 读取命中价格仅为原基础价的 **10%（立省 90%）**；
  - 响应体在 `message_start` 或 `message_delta` 返回 `usage.cache_read_input_tokens` 与 `usage.cache_creation_input_tokens`。
- **DeepSeek (V3 / R1)**：
  - 全自动最长公共前缀匹配：以 64-token chunk 为粒度自动在显存/NVMe 固态硬盘对齐；
  - 命中缓存价格低至 0.14 元 / 1M（**立省 86%**）；
  - 响应体在流式结束返回 `usage.prompt_cache_hit_tokens`。
- **OpenAI (GPT-4o, o1, o3-mini)**：
  - 全自动前缀匹配：以 128-token chunk 为粒度，最小 1,024 Tokens 自动对齐；
  - 读取命中价格为原基础价的 **50%（立省 50%）**；
  - 流式请求**必须显式声明 `"stream_options": {"include_usage": true}`**，否则流式结束不会下发 usage 报文；命中缓存在 `usage.prompt_tokens_details.cached_tokens` 中提供。

### 2.2 确定性 7 层 Token 排布流水线架构
要保证云端 100% 稳定命中 KV Cache，Prompt 必须保证从第 0 个 Token 开始的绝对连续性：
- **Layer 0-1 (Static Base)**：核心角色 + 按字典序排序的 Rules 与 Skills，换行符统一为 `\n`，100% 不可变；
- **Layer 2 (Canonical Tools)**：工具定义按名称字母排序，参数 Schema 经由 Canonical JSON 递归排序 Key；并在最后一个工具声明末尾挂载 **Breakpoint 1**；
- **Layer 3 (Workspace AST)**：按指纹缓存的工程架构骨架；
- **Layer 4-5 (Linear History & In-Place Pruning)**：历史会话保持单向正序追加（Append-Only）；当轮次 > 3 且单次工具输出 > 1,000 字符时，执行**原位折叠修剪（In-Place Output Pruning）**，既防止超长上下文爆栈，又完整保持前序拓扑与哈希一致性；并在倒数第 2 轮历史 User 消息挂载 **Breakpoint 2**；
- **Layer 6 (Dynamic Ephemeral Tail)**：动态技术栈感知、任务接续信息与待确认 Diff 文件列表，**严格剥离出 SystemPrompt，作为最新 User 消息尾部的 `<dynamic_context>` 附件注入**。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 步骤 1：规范化递归有序序列化器 (`pkg/protocol/canonical_json.go`)
提供 `MarshalCanonical(v any) ([]byte, error)` 与 `NormalizeNewlines(s string) string`，消灭 Windows `\r\n` 与 Go Map 遍历随机哈希：
```go
func CanonicalizeValue(v any) any {
	// 递归将 map keys 提取后通过 sort.Strings 排序并重构，规避字节漂移
}
```

### 步骤 2：历史工具输出双阈值原位修剪与正序追加 (`app.go`)
```go
func pruneHistoricalOutput(content string, maxChars int) string {
	if len(content) <= maxChars { return content }
	lines := strings.Split(content, "\n")
	if len(lines) <= 12 { return content[:maxChars] + "...\n[Output pruned for KV cache efficiency]" }
	head := strings.Join(lines[:8], "\n")
	tail := strings.Join(lines[len(lines)-2:], "\n")
	prunedLines := len(lines) - 10
	return fmt.Sprintf("%s\n\n[... %d lines folded/pruned for KV cache efficiency ...]\n\n%s", head, prunedLines, tail)
}
```
在 `buildConversationWindow` 中遍历历史消息，若 `len(history) > 3 && i < len(history)-2 && len(content) > 1000`，直接调用 `pruneHistoricalOutput` 原位替换，保持单向追加。

### 步骤 3：动态上下文物理沉底与 SystemPrompt 冻结 (`app_chat.go`)
1. `appendEnabledPolicies` 对 skills 和 rules 实施 `sort.Slice` 字典序排序；
2. `systemPrompt` 剥离任何动态技术栈或接续目标；
3. 将探测出的环境和任务状态统一收拢进 `<dynamic_context>...</dynamic_context>` 块，追加到当前最新一条 User 消息末尾。

### 步骤 4：厂商 Provider 协议升级与真实 Usage 捕获
1. **OpenAI 驱动 (`openai_provider.go`)**：
   - 注入 `"stream_options": {"include_usage": true}`；
   - 解析 `sseChunk.Usage.PromptCacheHitTokens` 及 `sseChunk.Usage.PromptTokensDetails.CachedTokens`，填入 `chunk.Usage.CacheReadTokens`。
2. **Anthropic 驱动 (`anthropic_provider.go`)**：
   - 注入 Header `"anthropic-beta": "prompt-caching-2024-07-31"`；
   - 工具列表末尾挂载 `cache_control: {"type": "ephemeral"}`（Breakpoint 1）；
   - 倒数第 2 轮历史 User 消息末尾挂载 `cache_control: {"type": "ephemeral"}`（Breakpoint 2）；
   - 解析 `message_start` 中的 `cache_read_input_tokens` 与 `cache_creation_input_tokens`。

### 步骤 5：真实遥测中心与顶栏微型指示胶囊
1. `internal/telemetry/tracker.go` 新增 `RecordWithCache`，准确记录 `CacheReadTokens`、`CacheCreationTokens`，按公式 $\text{CacheHitRate} = \frac{\text{CacheReadTokens}}{\text{PromptTokens}} \times 100\%$ 实时换算，绝无任何 Mock 假数据；
2. 前端 `ChatCockpit.vue` 顶栏增加微型指示胶囊：
   - 仅在有实际 Token 消耗时显示：`⚡ KV Cache 89% · 省 $1.20`；
   - 悬停浮层完整呈现命中率、读取缓存 Tokens、写入缓存 Tokens 与总 Prompt 分布；
   - 用户提问气泡中自动将 `<dynamic_context>` 折叠为极简徽章 `⚡ 运行时上下文快照`，保持 Warm Minimalist 界面清爽。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **绝对禁止在 System Prompt 中插值任何易变变量**：
   - 像任务接续状态、文件修改名单、当前时间戳等动态数据，一旦进入 System Prompt，就会导致整轮会话的最长公共前缀哈希失效。必须严格恪守**“静态前缀放 System，动态尾部放 User”**的物理隔离准则。
2. **OpenAI 协议流式推理不返回 Usage 陷阱**：
   - 在流式模式下，OpenAI 兼容端点默认不会下发 `usage` 块，必须显式传递 `"stream_options": {"include_usage": true}`，否则即使云端命中了 Prompt Caching，客户端也拿不到 `cached_tokens`。
3. **Anthropic 断点配额陷阱**：
   - Anthropic 单次请求最多只支持 4 个 `cache_control` 断点。推荐采用“两断点黄金法则”：1 个在不可变 Tools 结尾，1 个在倒数第 2 轮 User 消息结尾。避免在每个小 block 都乱打断点导致 400 Bad Request。
4. **铁律 0.5 零假数据执行纪律**：
   - 任何缓存指示胶囊和节省金额必须 100% 绑定来自上游 SSE 流下发的实际报文；在未产生实际请求前，前端保持干净真实的空状态。
