// Package migrations 以 embed 方式携带所有 .sql 迁移文件，
// 使二进制可独立部署（无需随附 migrations 目录）。
//
// 这是「单文件交付」的关键一环：迁移随编译进二进制，
// 启动时由 db.Migrate 从 FS 里读出并执行（见 internal/db/migrate.go）。
package migrations

import "embed"

// FS 包含本目录下全部 .sql 迁移，按文件名升序执行。
//
// ★ 命名即顺序：db.Migrate 用 fs.Glob 取出全部 *.sql 后 sort.Strings，
// 因此新增迁移必须以递增的序号前缀命名（如 0001_init.sql、0002_xxx.sql），
// 否则执行顺序会与预期不符。
//
//go:embed *.sql
var FS embed.FS
