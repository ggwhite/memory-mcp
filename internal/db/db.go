package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Memory 一筆持久記憶。
type Memory struct {
	ID      int64     `json:"id"`
	Type    string    `json:"type"`
	Content string    `json:"content"`
	Tags    string    `json:"tags"`
	Project string    `json:"project"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// ListOptions 列表查詢參數。
type ListOptions struct {
	Type  string
	Limit int
	Since string
}

// Stats 記憶統計。
type Stats struct {
	Total    int            `json:"total"`
	ByType   map[string]int `json:"by_type"`
	Earliest string         `json:"earliest"`
	Latest   string         `json:"latest"`
}

// DB SQLite 資料庫連線。
type DB struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS memories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT NOT NULL CHECK(type IN ('feedback','til','summary','knowledge')),
    content  TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '',
    project  TEXT NOT NULL DEFAULT '',
    created  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content, tags, project,
    content=memories, content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;
`

const timeLayout = "2006-01-02T15:04:05"

// Open 開啟或建立 SQLite 資料庫，自動建立目錄與 schema。
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// Close 關閉資料庫連線。
func (d *DB) Close() error {
	return d.db.Close()
}

// Store 儲存一筆記憶，回傳 auto-increment ID。
func (d *DB) Store(mem *Memory) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO memories (type, content, tags, project) VALUES (?, ?, ?, ?)`,
		mem.Type, mem.Content, mem.Tags, mem.Project,
	)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	return res.LastInsertId()
}

// Get 依 ID 取得一筆記憶。
func (d *DB) Get(id int64) (*Memory, error) {
	var m Memory
	var created, updated string
	err := d.db.QueryRow(
		`SELECT id, type, content, tags, project, created, updated FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	m.Created, _ = time.Parse(timeLayout, created)
	m.Updated, _ = time.Parse(timeLayout, updated)
	return &m, nil
}

// Update 更新指定記憶的內容。
func (d *DB) Update(id int64, content string) error {
	res, err := d.db.Exec(
		`UPDATE memories SET content = ?, updated = strftime('%Y-%m-%dT%H:%M:%S','now') WHERE id = ?`,
		content, id,
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("update: memory %d not found", id)
	}
	return nil
}

// Delete 刪除指定記憶。
func (d *DB) Delete(id int64) error {
	res, err := d.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("delete: memory %d not found", id)
	}
	return nil
}

func parseSince(s string) (int, error) {
	if !strings.HasSuffix(s, "d") {
		return 0, fmt.Errorf("invalid since format %q, expected Nd", s)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil {
		return 0, fmt.Errorf("invalid since format %q: %w", s, err)
	}
	return n, nil
}

// List 列出記憶，支援 type 過濾、since 時間範圍、limit 筆數。
func (d *DB) List(opts ListOptions) ([]Memory, error) {
	query := `SELECT id, type, content, tags, project, created, updated FROM memories`
	var args []any
	var where []string

	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, opts.Type)
	}
	if opts.Since != "" {
		days, err := parseSince(opts.Since)
		if err != nil {
			return nil, err
		}
		where = append(where, "created >= strftime('%Y-%m-%dT%H:%M:%S','now', ?)")
		args = append(args, fmt.Sprintf("-%d days", days))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created DESC, id DESC"
	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated); err != nil {
			return nil, fmt.Errorf("list scan: %w", err)
		}
		m.Created, _ = time.Parse(timeLayout, created)
		m.Updated, _ = time.Parse(timeLayout, updated)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// Stats 回傳記憶統計資訊。
func (d *DB) Stats() (*Stats, error) {
	s := &Stats{ByType: make(map[string]int)}

	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&s.Total); err != nil {
		return nil, fmt.Errorf("stats total: %w", err)
	}
	if s.Total == 0 {
		return s, nil
	}

	rows, err := d.db.Query(`SELECT type, COUNT(*) FROM memories GROUP BY type`)
	if err != nil {
		return nil, fmt.Errorf("stats by_type: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, fmt.Errorf("stats scan: %w", err)
		}
		s.ByType[typ] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.db.QueryRow(`SELECT MIN(created) FROM memories`).Scan(&s.Earliest)
	d.db.QueryRow(`SELECT MAX(created) FROM memories`).Scan(&s.Latest)
	return s, nil
}

// ExportAll 匯出所有記憶。
func (d *DB) ExportAll() ([]Memory, error) {
	return d.List(ListOptions{})
}

// ImportBatch 批次匯入記憶，回傳成功筆數。
func (d *DB) ImportBatch(memories []Memory) (int, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("import begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO memories (type, content, tags, project) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("import prepare: %w", err)
	}
	defer stmt.Close()

	var count int
	for _, m := range memories {
		if _, err := stmt.Exec(m.Type, m.Content, m.Tags, m.Project); err != nil {
			return count, fmt.Errorf("import row: %w", err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("import commit: %w", err)
	}
	return count, nil
}
