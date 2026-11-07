package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// UsageCounter 以 llm_usage table 記錄每日 LLM 呼叫次數，實作 llm.Counter。
type UsageCounter struct {
	db *sql.DB
}

// LLMUsage 回傳綁定此 DB 的每日 LLM 呼叫計數器。
func (d *DB) LLMUsage() UsageCounter {
	return UsageCounter{db: d.db}
}

// Incr 把 day（YYYY-MM-DD）的呼叫次數原子地加一，回傳加完後的值。
func (c UsageCounter) Incr(day string) (int, error) {
	var n int
	err := c.db.QueryRow(
		`INSERT INTO llm_usage (day, calls) VALUES (?, 1)
		 ON CONFLICT(day) DO UPDATE SET calls = calls + 1
		 RETURNING calls`, day,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("llm usage incr: %w", err)
	}
	return n, nil
}

// Calls 回傳 day 已用的呼叫次數，沒有紀錄為 0。
func (c UsageCounter) Calls(day string) (int, error) {
	var n int
	err := c.db.QueryRow(`SELECT calls FROM llm_usage WHERE day = ?`, day).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("llm usage: %w", err)
	}
	return n, nil
}
