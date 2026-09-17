# 49. 多协议网关、OAuth 2.0 刷新机制与 new-api 极简单输入框鉴权体验对齐

> **归档时间**：2026-09-17  
> **分类领域**：模型网关 / OAuth 2.0 鉴权 / Google RFC 6749 规范 / 凭据治理 / UI人机工程学  
> **核心标签**：`Refresh Token`、`Google OAuth`、`new-api 源码解密`、`Client ID 绑定`、`单输入框直贴凭据`

---

## ① 知识点与问题背景 (Context & Problem Statement)

### 1.1 业务诉求演进
在主流 AI Coding 桌面与网关中，大模型渠道鉴权形式日益多样化：
1. **静态 API Key**：OpenAI (`sk-...`)、Anthropic Claude (`sk-ant-...`)、Google Gemini (`AIzaSy...`)；
2. **本地免鉴权**：Ollama (`http://localhost:11434`) 或内网 vLLM / SGLang 私有化实例；
3. **企业与云厂商专有凭据**：Azure OpenAI（32位 Key + API Version）；
4. **OAuth 2.0 Refresh Token (RT)**：ChatGPT Subscription / OpenAI Codex、Google Cloud / Vertex AI 等。

在初期迭代中，当启用 Refresh Token 模式时，前端弹窗直接展开了 4 个输入框：`Refresh Token`、`Client ID`、`Client Secret`、`Token 刷新端点`。
这引发了真实用户的强烈疑问：**“一个要填写吗？你看看 new-api 怎么做的？”** 同时，用户提供了一个形如 `1//06N5...` 的 Google Refresh Token，希望验证能否直接成功换票。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 2.1 RFC 6749 OAuth 2.0 Refresh Token 规范与 Google 的强制要求
在标准 OAuth 2.0（RFC 6749 第 6 节）中，客户端使用 Refresh Token 刷新 Access Token 时，向授权服务器端点发出 `POST` 请求：
```http
POST /token HTTP/1.1
Host: oauth2.googleapis.com
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&
refresh_token=1//06N5...&
client_id=...&
client_secret=...
```

#### 为什么 Google RT 绝对不能“单凭一个 RT 字符串”刷新？
我们通过真实 Go 代码向 `https://oauth2.googleapis.com/token` 发起探针验证：
1. **不带 client_id**：Google 直接返回 `HTTP 400 Bad Request`：
   ```json
   {
     "error": "invalid_request",
     "error_description": "Could not determine client ID from request."
   }
   ```
2. **带入 Google Cloud SDK 官方公开 client_id (`764086051850-...`)**：Google 返回 `HTTP 401 Unauthorized`：
   ```json
   {
     "error": "unauthorized_client",
     "error_description": "Unauthorized"
   }
   ```
**根本原因**：
* 在 Google OAuth 2.0 体系中，每个 Refresh Token 在用户授权同意（Consent）的那一刻，都通过密码学签名与创建该会话的 **具体 OAuth 2.0 客户端（Client ID）严格双向绑定**；
* 如果一个 RT 是由某个特定应用（如用户的某个 GCP 项目、或者某个第三方开源 CLI）颁发的，授权服务器严格核验该 RT 是否归属于请求中的 `client_id`。如果客户端 ID 不匹配，Google 就会坚决返回 `unauthorized_client`，防止跨客户端盗用凭据。

### 2.2 new-api 源码架构深度解密：为什么 new-api 只用“一个输入框”？
我们查阅了 `new-api`（`E:\new-api`）的完整前端与后端源码：

1. **Google Gemini (Channel 24)**：
   * 在 `new-api/relay/channel/gemini/adaptor.go` 中，标准 Gemini 走 Google AI Studio 的 OpenAPI 模式，请求头传递 `x-goog-api-key: info.ApiKey`；
   * 用户在 `Key (密钥)` 输入框中只需填入一个 `AIzaSy...` 格式的 API Key，根本不需要碰 OAuth。
2. **Google Cloud / Vertex AI (Channel 41)**：
   * 在 `new-api/web/src/features/channels/lib/channel-type-config.ts` 中，`Key` 的提示文字为：
     ```typescript
     key: 'Service account JSON or API key'
     ```
   * 在 `new-api/relay/channel/vertex/adaptor.go` 中：
     ```go
     adc := &Credentials{}
     if err := common.Unmarshal([]byte(info.ApiKey), adc); err != nil {
         return "", fmt.Errorf("failed to decode credentials file: %w", err)
     }
     ```
   * **设计精髓**：`new-api` **从来没有让用户分别输入 client_id、secret、project_id**！用户从 GCP 后台下载 Service Account JSON，或者从本机 `application_default_credentials.json` 复制全文，**整段 JSON 直接粘贴进单个 Key 输入框**！`new-api` 自动提取其中的全部字段。
3. **ChatGPT Subscription / Codex OAuth (Channel 57)**：
   * 在 `new-api/service/codex_oauth.go` 中：
     ```go
     const codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
     const codexOAuthTokenURL = "https://auth.openai.com/oauth/token"
     ```
   * `new-api` 内置了官方公开的 Client ID，所以用户如果只粘贴 RT，也能直接免填 Client ID 成功换票；如果用户粘贴包含 `access_token` 的整段 JSON，也能直接解析使用。

**结论**：`new-api` 的卓越人机工程学在于：**永远保持单输入框主界面，通过智能 JSON 嗅探与平台默认值抹平复杂度，绝不迫使开发者逐项填写四五个散碎字段**。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 3.1 后端微内核：增强 `ExtractedOAuthCredentials` 与凭据自动嗅探
在 `internal/config/credential.go` 中：
1. 内置 OpenAI Codex 官方客户端 ID 与端点常量；
2. 扩展 `ParseOAuthCredentialsFromRaw`，不仅支持 `refresh_token`，还完整解析 `access_token`、`client_id`、`client_secret`、`token_uri`、`project_id`；
3. 在 `ResolveChannelCredentials` 中增加智能嗅探：即使渠道的 `auth_type` 停留在 `api_key`，只要用户在 Key 框中粘贴了以 `{` 开头且包含 OAuth 凭据的 JSON，微内核全自动识别并无感转入换票保活流程。

### 3.2 前端界面：极简单一凭据框 + 可折叠高级参数抽屉
在 `frontend/src/App.vue` 中重构渠道表单：
1. **主输入框**：单一多行文本框，清晰提示支持 API Key、多 Key 轮询或直贴凭据 JSON；
2. **高级覆盖参数**：通过 `<details>` 原生语义标签默认折叠 `Token 端点`、`Client ID`、`Client Secret`，仅供私有 OAuth 网关按需展开覆盖；
3. 普通开发者只需粘贴一次 JSON 或 Key，即可一键保存并测速。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **Google 凭据的正确导入方式**：
   * 若使用 Google AI Studio：直接获取 `AIzaSy...` 格式的 API Key 填入即可，最稳定简单；
   * 若使用 Google Cloud / Vertex AI：执行 `gcloud auth application-default login`，找到 `%APPDATA%\gcloud\application_default_credentials.json`，将其内容整体复制粘贴进 Key 输入框，系统将自动读取 `client_id` 与 `refresh_token` 完成换票。
2. **Fail-Closed 错误人话反馈**：
   * 当检测到 `1//` 前缀的 Google RT 且无 Client ID 时，不可返回空指针或模糊的 400 错误，必须明确提示：`"检测到 Google OAuth Refresh Token，Google 要求必须提供 Client ID (建议直接将 Google 凭据 JSON 完整粘贴在密钥框中)"`，引导用户正确配置。
3. **构建闭环铁律**：
   * 每次调整前后端通信协议或凭据模型后，必须依次完成 `go test ./...`、`go run ./tools/archcheck`、`npm run build`，并最终执行 `powershell -File scripts/build-windows.ps1` 产出最新安装包，确保安装向导始终携带发货级代码。
