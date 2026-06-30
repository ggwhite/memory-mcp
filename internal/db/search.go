package db

import (
	"fmt"
	"time"
)

// SearchOptions 搜索查詢參數。
type SearchOptions struct {
	Query string
	Type  string
	Limit int // default 5
}

// SearchResult 搜索結果，包含 FTS5 排名分數。
type SearchResult struct {
	Memory
	Rank float64 `json:"rank"`
}

// Search 使用 FTS5 全文搜索記憶，依相關度排序。
func (d *DB) Search(opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT m.id, m.type, m.content, m.tags, m.project, m.created, m.updated, f.rank
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?`
	args := []any{opts.Query}

	if opts.Type != "" {
		query += ` AND m.type = ?`
		args = append(args, opts.Type)
	}

	query += ` ORDER BY f.rank`

	limit := opts.Limit
	if limit <= 0 {
		limit = 5
	}
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var created, updated string
		if err := rows.Scan(&r.ID, &r.Type, &r.Content, &r.Tags, &r.Project, &created, &updated, &r.Rank); err != nil {
			return nil, fmt.Errorf("search scan: %w", err)
		}
		r.Created, _ = time.Parse(timeLayout, created)
		r.Updated, _ = time.Parse(timeLayout, updated)
		results = append(results, r)
	}
	return results, rows.Err()
}
