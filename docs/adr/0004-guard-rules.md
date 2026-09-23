# ADR-0004：架构守卫规则与演进方式

日期：2026-09-23 ｜ 状态：已接受

## 背景

旧项目分层靠约定与自觉，越层/吞错/超时缺失靠事后人肉核验发现；守卫脚本（arch_check）在旧仓库证明有效但引入太晚。重建版要求**守卫从第一天存在**。

## 决策

`scripts/arch_check.ps1` 维护以下规则（提交前与 CI 必须通过）：

| 规则 | 内容 | 针对的旧病 |
| --- | --- | --- |
| R1 | `internal/core/**` 禁止 import 编排/适配器/壳/wails | 内核被反向拖入业务依赖 |
| R2 | `internal/**` 非测试文件禁止 `_ = xxx.`（丢弃 error 返回值） | 静默吞错丢消息（app_chat.go:282/503） |
| R3 | platform 下定义 `Execute` 的文件必须出现 `WithTimeout/WithDeadline` | 工具无超时契约（60s 硬杀） |
| R4 | `internal/` 每个包必须有 `// Package ` 包级注释 | 包职责无文档、边界靠猜 |

配套 CI 门禁：`gofmt -l` 空输出、`go vet`、golangci-lint（package-comments/exported）、`go test ./...`。

## 演进规则

1. 新增/修改守卫规则必须先更新本 ADR（追加"变更记录"）再改脚本，同 PR 提交；
2. 守卫只增不减：旧规则证明误报可放宽判定条件，但不得删除规则本身；
3. 守卫脚本保持 **ASCII-only**（Windows PowerShell 5.1 按 ANSI 读 UTF-8 无 BOM 脚本会乱码解析失败——M0 实测教训）；
4. 守卫是"防回退"而非"防创新"：规则只约束已发生过事故的形态，不限制新架构探索。

## 变更记录

- 2026-09-23 初版 R1-R4。
