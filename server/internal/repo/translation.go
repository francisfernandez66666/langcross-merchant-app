package repo

import (
	"context"
	"fmt"
	"strings"
)

// LookupTranslations 从翻译记忆（TM）里批量取译文。
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
func (r *Repo) TranslationCount(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM translation_cache`).Scan(&n)
	return n, err
}
