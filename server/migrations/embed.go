// Package migrations 以 embed 方式携带所有 .sql 迁移文件，
// 使二进制可独立部署（无需随附 migrations 目录）。
package migrations

import "embed"

// FS 包含本目录下全部 .sql 迁移，按文件名升序执行。
//
//go:embed *.sql
var FS embed.FS
