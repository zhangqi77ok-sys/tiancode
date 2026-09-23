//go:build windows && !installer_payload

package main

// appBinary 空占位：未启用 installer_payload 标签时（常规开发构建）载荷不存在。
// 为什么这样设计：go:embed 无法容忍缺失文件，而载荷是构建产物（不入库），
// 用构建标签把"需要载荷"这一步收敛到 release.ps1，保证 go build ./... 恒可过。
// 运行本占位构建的安装器会明确报错"安装包损坏"（见 doInstall），不会静默装错。
var appBinary []byte
