// Package db 负责数据库连接与迁移。
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
