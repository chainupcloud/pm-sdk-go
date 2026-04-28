//go:build tools

// Package tools 通过 build tag 隔离仅 build-time 使用的工具，
// 让 `go mod tidy` 不会把它们当成运行时依赖丢弃。
//
// 仅 `scripts/codegen.sh` 引用本文件下的二进制。
package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
