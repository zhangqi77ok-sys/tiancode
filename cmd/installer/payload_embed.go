//go:build windows && installer_payload

package main

import _ "embed"

// appBinary 是内嵌的应用程序（release.ps1 构建安装包时启用本标签）。
//
//go:embed payload/tiancode.exe
var appBinary []byte
