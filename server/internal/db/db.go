// Package db 负责数据库连接与迁移。
//
// 技术选型：PostgreSQL + database/sql 标准接口，不引入 ORM。
// 驱动用 pgx 的 database/sql 适配层（github.com/jackc/pgx/v5/stdlib），
// 只暴露 *sql.DB，保持上层对驱动无感知。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// pgx 的 database/sql 适配层
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open 建立连接池并做一次 Ping 探活。
//
// 连接池参数取的是「单机小规模经营 SaaS」的保守值：
//   - 最大 20 个连接：避免打满本地 PostgreSQL 的默认上限；
//   - 空闲保留 5 个：减少频繁建连开销；
//   - 连接最长存活 1 小时：定期重建，规避长连接被中间设备静默断开。
//
// Ping 失败会主动关闭连接池并返回错误，避免把坏连接池交给上层。
func Open(dsn string) (*sql.DB, error) {
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	d.SetMaxOpenConns(20)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return d, nil
}
