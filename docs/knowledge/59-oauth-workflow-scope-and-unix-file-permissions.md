# 知识点 59: GitHub OAuth workflow 作用域推送阻断陷阱与 Unix 凭据目录 0700/0600 最小权限规范

> **归档时间**：2026-09-21  
> **涉及模块**：配置与凭据持久化 (`internal/config`)、Git 版本控制与 CI/CD 流水线 (`.github/workflows`)  
> **核心标签**：OAuth 权限 / workflow scope / 最小权限原则 / 0700与0600 / 跨平台安全

---

## ① 知识点与问题背景 (Context & Problem Statement)

在完善项目的跨平台安全防护规范与持续集成运维配置时，遇到了两个典型的工程陷阱：

1. **跨平台凭据目录权限过宽**：
   项目原本在 Windows 平台采用 DPAPI 加密 API Key，但底层配置目录及配置文件使用的是标准 `0755` 目录权限与 `0644` 文件权限。在 Linux/macOS 跨平台部署或多用户环境下，这会导致凭据目录成为其他系统用户可读的状态，违背了凭据与会话资产的最小权限防护原则（Least Privilege Principle）。
2. **Git 推送遭遇 GitHub OAuth 403 阻断**：
   在向 GitHub 远程仓库推送修改了 `.github/workflows/ci.yml` 的 commit 时，Git 客户端抛出如下致命错误：
   ```text
   ! [remote rejected] main -> main (refusing to allow an OAuth App to create or update workflow `.github/workflows/ci.yml` without `workflow` scope)
   error: failed to push some refs to 'https://github.com/zhangqi77ok-sys/tiancode.git'
   ```
   导致常规的推送直接被远端拦截阻断。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. GitHub OAuth 令牌的 `workflow` 作用域隔离机制
GitHub 为了防止恶意第三方应用或仅具有代码读写权限的 OAuth Token 篡改自动化部署脚本，进而通过 CI/CD 窃取仓库 Secrets（如 `GITHUB_TOKEN`、发布密钥、云凭据），特别对 `.github/workflows/` 路径下的任意文件实行了强隔离：
- 普通包含 `repo` 作用域的 OAuth App 令牌在尝试创建或更新 `.github/workflows/*.yml` 时，GitHub 远端服务端会在 Pre-receive 钩子中主动校验 Token 的 Scope；
- 若 Scope 中缺少显式的 `workflow` 权限，服务端将直接返回 `refusing to allow an OAuth App to create or update workflow ... without workflow scope`。

### 2. POSIX 文件系统掩码与 0700/0600 最小特权基线
在 Unix-like 系统中：
- 目录权限 `0700` (`rwx------`)：仅当前属主拥有读、写以及进入（检索）目录的权限，同组用户及其他用户均无法列举或进入该目录；
- 文件权限 `0600` (`rw-------`)：仅当前属主拥有读写权限，杜绝其他用户窥探私密配置文件；
- 在 Windows 系统中，NTFS 的 ACL 机制与 POSIX 权限有所映射，但底层使用 `0700` 与 `0600` 不会对 Windows 原生运行造成任何冲突与异常，从而实现跨平台的一致性保护。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 步骤 1：全域收敛凭据与配置持久化权限至 0700 / 0600

1. 在 `internal/config/extra_stores.go` 中规范原子写入函数：
   ```go
   func atomicWriteConfig(filePath string, data []byte) error {
       if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
           return err
       }
       tmpPath := fmt.Sprintf("%s.tmp.%d", filePath, time.Now().UnixNano())
       if err := os.WriteFile(tmpPath, data, 0600); err != nil {
           return err
       }
       if err := os.Rename(tmpPath, filePath); err != nil {
           _ = os.Remove(filePath)
           if renameErr := os.Rename(tmpPath, filePath); renameErr != nil {
               _ = os.Remove(tmpPath)
               return os.WriteFile(filePath, data, 0600)
           }
       }
       return nil
   }
   ```
2. 在 `internal/config/paths.go`、`channel_store.go`、`projects.go` 与 `internal/session/store.go` 中，将用户级私有目录 `~/.tiancode` 与 `~/.tiancode/sessions` 的 `os.MkdirAll` 权限由 `0755` 统一更正为 `0700`。
3. 编写 `internal/config/secret_perm_test.go` 针对非 Windows 系统进行 `info.Mode().Perm()` 断言，确保单测红绿灯闭环。

### 步骤 2：优雅处置 GitHub workflow 权限阻断

当本地 OAuth Token 缺少 `workflow` 权限时：
1. **立即解耦提交**：
   使用 `git reset --soft HEAD~1` 将提交回退为暂存区修改；
2. **还原 workflow 文件**：
   执行 `git checkout HEAD -- .github/workflows/` 撤销对工作流文件的改动；
3. **安全提交业务代码**：
   重新提交业务与测试代码并执行 `git push origin main` 确保正常功能合规推送；
4. **提升 OAuth Scope 或由拥有特权的人类维护者修改工作流**：
   若需更改 `.github/workflows/`，必须在 GitHub Developer Settings 中重新为该 OAuth Token / Personal Access Token (PAT) 勾选 `workflow` 作用域，或由仓库拥有者通过 Web UI / 具名 PR 直接合入。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **自动化脚本勿触碰 `.github/workflows`**：
   在自动化 Agent 运行期间，尽量将变更集中在业务代码、单元测试与产品文档中，避免未经特权授权修改 CI/CD 流水线导致推送被阻断。
2. **不要假设 Windows 会自动屏蔽权限错误**：
   虽然 Windows 不依赖 POSIX 权限位做强隔离，但代码必须严谨指定 `0700` 和 `0600`，确保同一套微内核在跨平台移植或开发者切换环境时无缝继承高等级防护标准。
3. **临时文件同样必须遵循权限规范**：
   在执行原子写入（`atomicWriteConfig` / `atomicWriteSession`）时，临时生成的 `.tmp` 文件如果使用 `0644` 可能会发生短暂的时间差权限泄漏，必须在创建之初就传入 `0600`。
