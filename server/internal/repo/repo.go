// Package repo 是数据访问层：所有 SQL 集中在此，向上只暴露领域对象。
package repo

import "database/sql"

// Repo 持有连接池，供各 *Entity* 方法使用。
type Repo struct{ DB *sql.DB }

// New 构造数据访问层。
func New(db *sql.DB) *Repo { return &Repo{DB: db} }
