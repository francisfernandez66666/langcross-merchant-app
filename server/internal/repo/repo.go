// Package repo 是数据访问层：所有 SQL 集中在此，向上只暴露领域对象。
//
// 在整体链路里的位置：httpapi（路由/鉴权）→ service（翻译/OCR 等外部能力）
// → repo（本包，PostgreSQL 读写）→ domain（领域模型）。本包不感知 HTTP，
// 也不编排业务流程，只负责「表 ↔ domain 结构体」的映射与最小必要的约束校验。
//
// 职责边界（哪些事不在这里做）：
//   - 「原文未确认不允许翻译」「券必须属于本店」等业务规则由上层负责，
//     本包只保证单条 SQL 的正确性；调用方需自行校验后再落库。
//   - 不引入 ORM：SQL 全部手写，列清单用包级常量（如 storeCols / dishCols）
//     复用，避免 SELECT 与 Scan 的列序不一致。
//
// 全局约定与坑：
//   - 查不到记录统一返回哨兵错误 ErrNotFound，调用方一律用 errors.Is 判断。
//   - 可变参数的 IN (...) 查询需动态拼接占位符（参数个数不定），见 translation.go。
//   - 局部更新用 COALESCE($n, col) 实现「传 nil 即不改」，见 store.go / dish.go。
//   - JSONB 列在 Go 侧以 map / 结构体出入，序列化细节封装在各 scan* 辅助里。
package repo

import "database/sql"

// Repo 持有连接池，供各 *Entity* 方法使用。自身无状态，可安全并发调用。
type Repo struct{ DB *sql.DB }

// New 构造数据访问层。DB 的生命周期由调用方管理，Repo 不负责关闭连接池。
func New(db *sql.DB) *Repo { return &Repo{DB: db} }
