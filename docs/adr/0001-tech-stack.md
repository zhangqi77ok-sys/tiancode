# ADR-0001：技术栈选型 Go + Wails + Vue3

日期：2026-09-23 ｜ 状态：已接受

## 背景

旧 tiancode 决定清空重建。语言与框架需要重新确认（此前会话已完成系统评估）。

## 决策

Go 1.22 + Wails v2 + Vue3 + Tailwind CSS 4 + Pinia。

## 理由

- **单 exe 交付**是产品硬需求（个人 Windows 工具），Go/Wails 走系统 webview，包体 10MB 级；
- 前端 Vue3 组件与设计令牌可从旧版复用概念；
- 本会话已完整摸清旧实现全部坑（流式无纪律/持久化脆弱/超时硬编码），重建有明确靶子；
- 已评估并否决：TS+Electron（包体 150MB+，重写量最大，收益在 gate 触发前兑现不了）；Rust+Tauri（零语言储备，Tauri 对 Wails 无包体优势）；Python（分发硬伤）。

## 后果

- 壳与内核以 Go 接口解耦；未来若 TS 壳有真实收益，沿网关边界替换（M2 起绑定层保持薄）；
- wails 依赖经 `vendor/` 入库保证离线构建；
- 若未来 3 个月 roadmap 中 ≥3 个大特性依赖 npm-only 生态，重开 ADR 评估换壳。
