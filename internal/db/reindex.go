package db

import (
	"context"
	"fmt"
)

// ReindexStats 補算 embedding 的執行結果統計。
type ReindexStats struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Failed    int `json:"failed"`
	// FirstErr 第一筆失敗的原因，全部成功時為 nil。
	FirstErr error `json:"-"`
}

// Reindex 為缺少 embedding、或 embedding 使用舊模型的記憶補算向量。embedder
// 未設定時直接回傳全 0 統計，不視為錯誤。可重複執行，每次只處理缺漏或過期
// 的部分。
func (d *DB) Reindex(ctx context.Context) (ReindexStats, error) {
	var stats ReindexStats
	if d.embedder == nil {
		return stats, nil
	}

	rows, err := d.db.Query(`
		SELECT m.id, m.content
		FROM memories m
		LEFT JOIN memory_embeddings e ON e.memory_id = m.id
		WHERE e.memory_id IS NULL OR e.model != ?`, d.embedder.Model())
	if err != nil {
		return stats, fmt.Errorf("reindex query: %w", err)
	}

	type pending struct {
		id      int64
		content string
	}
	var items []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.content); err != nil {
			rows.Close()
			return stats, fmt.Errorf("reindex scan: %w", err)
		}
		items = append(items, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}
	stats.Total = len(items)

	for _, p := range items {
		vec, err := d.embedder.Embed(ctx, p.content)
		if err == nil {
			err = d.upsertEmbedding(p.id, vec, d.embedder.Model())
		}
		if err != nil {
			stats.Failed++
			if stats.FirstErr == nil {
				stats.FirstErr = fmt.Errorf("memory #%d: %w", p.id, err)
			}
			continue
		}
		stats.Processed++
	}
	return stats, nil
}
