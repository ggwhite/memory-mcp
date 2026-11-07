package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrSupersededNotFound 被取代（或被合併刪除）的舊記憶 id 不存在；用 errors.Is 判斷。
var ErrSupersededNotFound = errors.New("superseded memory not found")

// StoreMerged 寫入一筆合併後的記憶（created 沿用 mem.Created，零值則為現在），並在同一個
// transaction 內刪除 ids。任一 id 不存在就整筆失敗、不做任何變更。供 compact 使用。
func (d *DB) StoreMerged(mem *Memory, ids []int64) (int64, error) {
	var created string
	if !mem.Created.IsZero() {
		created = mem.Created.UTC().Format(timeLayout)
	}
	return d.storeReplacing(mem, created, ids)
}

// storeReplacing 驗證 ids 全部存在後，於單一 transaction 寫入 mem 並刪除 ids；embedding
// 由 memory_embeddings 的 ON DELETE CASCADE 一併刪除。created 為空字串時用現在時間。
// mem.Supersedes 會回寫成去重後實際刪除的 id。
func (d *DB) storeReplacing(mem *Memory, created string, ids []int64) (int64, error) {
	clean, n := Sanitize(mem.Content)
	if clean == "" {
		return 0, ErrEmptyAfterSanitize
	}
	ids = dedupeIDs(ids)

	tx, err := d.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store begin: %w", err)
	}
	defer tx.Rollback()

	if err := checkIDsExist(tx, ids); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO memories (type, content, tags, project, created, source)
		 VALUES (?, ?, ?, ?, COALESCE(NULLIF(?, ''), strftime('%Y-%m-%dT%H:%M:%S','now')), ?)`,
		mem.Type, clean, mem.Tags, mem.Project, created, sourceOrUser(mem.Source),
	)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	for _, old := range ids {
		if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, old); err != nil {
			return 0, fmt.Errorf("delete superseded #%d: %w", old, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store commit: %w", err)
	}

	mem.Content = clean
	mem.Redacted = n
	mem.Supersedes = ids
	d.tryEmbed(id, mem.Content)
	return id, nil
}

// checkIDsExist 任一 id 不存在就回錯誤，錯誤訊息列出所有缺少的 id。
func checkIDsExist(tx *sql.Tx, ids []int64) error {
	var missing []string
	for _, id := range ids {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM memories WHERE id = ?`, id).Scan(&one)
		if err == sql.ErrNoRows {
			missing = append(missing, fmt.Sprintf("#%d", id))
			continue
		}
		if err != nil {
			return fmt.Errorf("check superseded #%d: %w", id, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrSupersededNotFound, strings.Join(missing, " "))
	}
	return nil
}

// dedupeIDs 去除重複 id，保留第一次出現的順序。
func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// SummariesOlderThan 列出 created 早於 olderThan（Nd 格式）的 summary，依 created、id 由舊到新；
// project 非空時只列該 project。
func (d *DB) SummariesOlderThan(olderThan, project string) ([]Memory, error) {
	days, err := parseSince(olderThan)
	if err != nil {
		return nil, err
	}
	query := selectMemoryCols + ` WHERE type = 'summary' AND created < strftime('%Y-%m-%dT%H:%M:%S','now', ?)`
	args := []any{fmt.Sprintf("-%d days", days)}
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY created ASC, id ASC`
	out, err := d.queryMemories(query, args...)
	if err != nil {
		return nil, fmt.Errorf("summaries older than: %w", err)
	}
	return out, nil
}
