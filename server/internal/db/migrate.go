// Package db 负责数据库连接与迁移。
//
// ── migrate.go：内嵌式自动迁移 ──────────────────────────────
//
// 迁移文件由 migrations 包以 go:embed 打进二进制（见 migrations/embed.go），
// 因此部署时只需一个可执行文件，无需随附 migrations 目录。
// 启动时按 schema_migrations 表判断哪些迁移尚未执行，只跑增量部分。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"

	"github.com/francisfernandez66666/langcross-merchant-app/server/migrations"
)

// Migrate 按文件名升序执行尚未应用的 .sql 迁移，并记录到 schema_migrations。
// 迁移文件自带 BEGIN/COMMIT，此处整段执行。
//
// 执行流程：
//  1. 建 schema_migrations 表（若不存在）——版本账本；
//  2. 读出已应用的版本集合；
//  3. 从内嵌 FS 取全部 *.sql，按文件名升序排序（因此迁移文件需以
//     0001_、0002_ 之类的序号前缀命名，保证顺序即依赖顺序）；
//  4. 跳过已应用的，逐个执行并记账。
//
// ★ 约定：迁移文件内部自带 BEGIN/COMMIT，本函数整段 ExecContext 执行，
// 因此不额外包事务；也意味着单个迁移要么整体成功、要么整体失败。
// 注意「执行成功」与「记账成功」是两步，若在两步之间崩溃可能重复执行，
// 所以迁移语句本身应写成幂等（IF NOT EXISTS 之类）。
//
// ★ 并发：多个实例同时启动会同时跑同一份迁移，DDL 之间互相等锁——
// 实测两个实例同时起 0007，一个直接 "deadlock detected" 退出（SQLSTATE 40P01）。
// 因此这里用一把**会话级 advisory lock** 把「跑迁移」整段串行化：
// 抢到的实例执行，其它实例阻塞等它跑完；等它们进来时 schema_migrations
// 已是最新，直接跳过。锁必须绑在**同一条连接**上（会话级锁属于连接），
// 故本函数全程复用一条 Conn，而不是从连接池里随手取。
func Migrate(ctx context.Context, d *sql.DB) error {
	conn, err := d.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Close()

	// 锁键取固定常量即可（只要所有实例一致）：'lc_migrate' 的 ASCII 前缀。
	const migrateLockKey int64 = 0x6C63_6D69_6772_6174 // "lcmigrat"
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	// 解锁用 Background：ctx 已取消也要把锁还回去，否则会锁死后续所有实例。
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLockKey)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("select schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, name := range files {
		if applied[name] {
			continue
		}
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES ($1)`, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		fmt.Printf("[migrate] applied %s\n", name)
	}
	return nil
}
