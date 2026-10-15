// Package repo 的数据访问实现之一：翻译记忆（translation_cache，简称 TM）。
//
// TM 是「原文 + 目标语 → 译文」的缓存表，翻译前先查、翻译后回写，命中即省掉一次
// 外部引擎调用（省钱且提速）。菜名、分类名这类词重复率高，TM 收益明显，也是
// 知识库/翻译记忆设计的一部分。唯一键 (source_lang, target_lang, source_text)
// 保证同一对语言下同一原文只有一条译文。
package repo

import (
	"context"
	"fmt"
	"strings"
)

// LookupTranslations 从翻译记忆（TM）里批量取译文。
//
// 一次查多条原文（一次网络往返），返回 {原文: 译文}，未命中的键不出现在结果里
// （调用方据此判断哪些还需送外部引擎）。
//
// 为什么占位符要动态拼接：IN (...) 的元素个数取决于 texts 长度，是运行时才知道的，
// 无法预先写死；故按数量生成 $3,$4,... 并用 strings.Join 拼进 SQL。这里只拼占位符
// （不是拼值），原文仍走参数绑定，因此不存在 SQL 注入。$1/$2 固定留给 from/to。
func (r *Repo) LookupTranslations(ctx context.Context, from, to string, texts []string) (map[string]string, error) {
	out := map[string]string{}
	if len(texts) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(texts))
	args := make([]any, 0, len(texts)+2)
	args = append(args, from, to)
	for i, t := range texts {
		placeholders[i] = fmt.Sprintf("$%d", i+3)
		args = append(args, t)
	}
	q := fmt.Sprintf(
		`SELECT source_text, target_text FROM translation_cache
		 WHERE source_lang = $1 AND target_lang = $2 AND source_text IN (%s)`,
		strings.Join(placeholders, ","))

	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src, dst string
		if err := rows.Scan(&src, &dst); err != nil {
			return nil, err
		}
		out[src] = dst
	}
	return out, rows.Err()
}

// SaveTranslations 把新译文写入 TM（重复则忽略，保证幂等）。
//
// 用多值 INSERT 一次性写入（每行 5 个占位符，数量随 pairs 变化故动态拼接；
// 仍走参数绑定，无注入风险）。ON CONFLICT DO NOTHING 依赖唯一键
// (source_lang, target_lang, source_text)：已存在的译文保持不动，重复回写安全。
// provider 记录译文来源（哪个引擎），便于后续追溯与替换。空 map 直接返回。
func (r *Repo) SaveTranslations(ctx context.Context, from, to string, pairs map[string]string, provider string) error {
	if len(pairs) == 0 {
		return nil
	}
	vals := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*5)
	i := 1
	for src, dst := range pairs {
		vals = append(vals, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)", i, i+1, i+2, i+3, i+4))
		args = append(args, from, to, src, dst, provider)
		i += 5
	}
	q := `INSERT INTO translation_cache(source_lang, target_lang, source_text, target_text, provider)
	      VALUES ` + strings.Join(vals, ",") + ` ON CONFLICT DO NOTHING`
	_, err := r.DB.ExecContext(ctx, q, args...)
	return err
}

// TranslationCount 返回 TM 中已缓存的条目数（健康检查 / 调试用）。
// 仅用于观察缓存规模，不参与业务判断。
func (r *Repo) TranslationCount(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM translation_cache`).Scan(&n)
	return n, err
}
