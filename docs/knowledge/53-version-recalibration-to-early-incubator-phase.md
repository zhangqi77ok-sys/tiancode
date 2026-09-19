# 53. 版本号校准为 0.0.1 与开发测试孵化阶段工程基线

## ① 知识点与问题背景 (Context & Problem Statement)

在项目迭代过程中，此前部分配置与脚本曾将版本号标记为 `2.0.0`。
然而从软件工程生命周期与发版严谨性来看：
1. **产品处于极早期孵化阶段**：核心执行回路、微内核插件热插拔体系、单向数据流与安全拦截刚完成收敛整肃，处于架构加固、密集提需求、自测与真实场景验证阶段；
2. **避免虚假成熟预期**：过早使用 `2.0.0` 或 LTS 标签会给团队和终端开发者带来误导性认知（误以为是生产就绪且接口锁定的稳定成熟期），违背了项目关于实事求是与零虚假承诺的核心价值观；
3. **语义化版本 (SemVer) 规范要求**：在 `0.y.z` 阶段，公共 API 与内部实现允许根据真实需求快速演进与重构，明确向所有协作者传达“当前正在进行基础建设与需求探索”的信号。

因此，必须将全链路发货版本从 `2.0.0` 统一定标重置为 `0.0.1`（即 `0.01` 开发测试与需求孵化阶段）。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 跨层级多语言单点版本一致性
桌面混合架构（Wails + Go + Vue + NSIS/纯 Go 安装器）跨越了多个技术栈，版本号如果分散在各处且未同步，极易造成运行期混乱：
- **Go 微内核层**：`app_config.go` 暴露给前端的 `RuntimeInfo.Version` 与 Release 检查回退文本；
- **Wails 宿主清单**：`wails.json` 中的 `info.productVersion`；
- **Node.js 依赖生态**：根目录 `package.json` 与前端 `frontend/package.json` 的 `version` 字段；
- **前端表现与状态层**：`workbench.ts` 的 `runtimeInfo` 响应式对象、`wailsBridge.ts` 的离线兜底运行时信息；
- **Windows 安装与注册表层**：`cmd/installer/main.go` 弹出的向导标题、Windows 注册表 `DisplayVersion` / `DisplayName` 卸载表项；
- **打包流水线与产物名称**：`scripts/build-windows.ps1` 产出的目标二进制可执行文件名 `bin/Tiancode_Setup_v0.0.1.exe`；
- **网络与协议报头**：`internal/config/credential.go` 发起 OAuth 请求时的 `User-Agent: tiancode/0.0.1`，以及 `internal/mcp/stdio.go` 握手报文的 `clientInfo.version`。

### 2. SemVer 规范中的 0.x.y 语义
按照语义化版本（Semantic Versioning 2.0.0）规范：
- `0.y.z`：初始开发阶段，任何事物都有可能改变，公共 API 处于实验与孵化阶段；
- `0.0.1`：最小可用骨架的起点，明确当前处于核心功能构建、测试套件完善与真实需求对齐期。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 全链路同步修改各层版本声明
1. **Wails & Node 配置**：
   - `wails.json`: `"productVersion": "0.0.1"`
   - `package.json`: `"version": "0.0.1"`
   - `frontend/package.json`: `"version": "0.0.1"`
2. **Go 微内核与网络层**：
   - `app_config.go`: `Version: "0.0.1"`，更新检查 fallback 改为 `"当前发货版本 0.0.1"`
   - `internal/config/credential.go`: `req.Header.Set("User-Agent", "tiancode/0.0.1")`
   - `internal/mcp/stdio.go`: `ClientInfo{ Name: "tcode-studio", Version: "0.0.1" }`
   - `internal/transport/http/server.go`: `"version": "0.0.1-DEV"`
   - `backend/cmd/tcode-daemon/main.go`: `Version = "0.0.1-DEV"`
3. **前端状态层**：
   - `frontend/src/stores/workbench.ts`: `runtimeInfo.version = '0.0.1'`
   - `frontend/src/core/wailsBridge.ts`: `getRuntimeInfo` fallback 返回 `version: '0.0.1'`
4. **安装器与构建脚本**：
   - `cmd/installer/main.go`:
     - 向导标题: `"湉码 v0.0.1 安装向导"`
     - 注册表: `DisplayName: "湉码 tiancode v0.0.1"`、`DisplayVersion: "0.0.1"`
     - 成功弹窗: `"✓ 湉码 v0.0.1 已成功安装到..."`
   - `scripts/build-windows.ps1`:
     - 输出产物指定为 `bin\Tiancode_Setup_v0.0.1.exe`

### 2. 闭环验证流程
```powershell
# 1. 前端类型检查与打包
cd frontend && npm run build && cd ..

# 2. Go 全域单元测试验证
go test ./...

# 3. 架构规范合规扫描
go run ./tools/archcheck
powershell -ExecutionPolicy Bypass -File scripts/arch_check.ps1

# 4. Windows 安装包全流程编译构建
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1
```

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **避免文档与代码脱节**：
   - 更新版本号时，必须同步检索文档中的安装文件名说明（如 `README.md`、`docs/V1_FEATURE_BOUNDARY_MATRIX.md`、`docs/PRODUCT_ONE_PAGER.md` 等），杜绝文档写 `v2.0.0` 但实际编译出 `v0.0.1` 的不一致现象；
2. **Windows 注册表 DisplayVersion 兼容性**：
   - Windows 控制面板和设置中心的“已安装的应用”依靠 `DisplayVersion` 字段展示，应保持标准的 `X.Y.Z` 格式（如 `0.0.1`），避免非数字前缀（如 `v0.01`），以防某些 Windows 注册表解析器截断；
3. **User-Agent 与 MCP ClientInfo 同步**：
   - 与外部模型服务、OAuth 验证端点及 MCP Server 交互时，报头与初始化 ClientInfo 带有版本号，保持一致有利于排查上游服务端日志与协议版本协商。
