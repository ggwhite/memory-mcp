package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ggwhite/memory-mcp/internal/embed"

	_ "modernc.org/sqlite"
)

// Memory 一筆持久記憶。
type Memory struct {
	ID       int64     `json:"id"`
	Type     string    `json:"type"`
	Content  string    `json:"content"`
	Tags     string    `json:"tags"`
	Project  string    `json:"project"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Redacted int       `json:"redacted,omitempty"`
	// Source 記憶的來源：user（預設）、llm:<provider/model>（LLM 產生）、compact（月度彙整）。
	Source string `json:"source,omitempty"`
	// Supersedes 只在 Store 時使用：這筆新記憶取代的舊記憶 id，會在同一個 transaction 內刪除。
	Supersedes []int64 `json:"supersedes,omitempty"`
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

// Store 記憶儲存後端的操作介面，*DB（本機 SQLite）與 httpapi.Client
// （透過 HTTP 轉發到遠端 memory-mcp）都實作此介面，讓 CLI／MCP server
// 可以不分本機或遠端統一呼叫。
type Store interface {
	Store(mem *Memory) (int64, error)
	Get(id int64) (*Memory, error)
	GetMany(ids []int64) ([]Memory, error)
	Timeline(id int64, before, after int) ([]Memory, error)
	Update(id int64, content string) error
	Delete(id int64) error
	List(opts ListOptions) ([]Memory, error)
	Search(opts SearchOptions) ([]SearchResult, error)
	Stats() (*Stats, error)
	Context(opts ContextOptions) (string, error)
	ExportAll() ([]Memory, error)
	ImportBatch(memories []Memory) (int, error)
	Close() error
}

// DB SQLite 資料庫連線。
type DB struct {
	db       *sql.DB
	embedder embed.Embedder
}

const schema = `
CREATE TABLE IF NOT EXISTS memories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT NOT NULL CHECK(type IN ('feedback','til','summary','knowledge')),
    content  TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '',
    project  TEXT NOT NULL DEFAULT '',
    created  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    source   TEXT NOT NULL DEFAULT 'user'
);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content, tags, project,
    content=memories, content_rowid=id,
    tokenize='trigram'
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

CREATE TABLE IF NOT EXISTS memory_embeddings (
    memory_id INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector    BLOB NOT NULL,
    model     TEXT NOT NULL,
    created   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE TABLE IF NOT EXISTS llm_usage (
    day   TEXT PRIMARY KEY,
    calls INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS observations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project       TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL DEFAULT 'feedback',
    content       TEXT NOT NULL,
    evidence      TEXT NOT NULL DEFAULT '[]',
    evidence_hash TEXT NOT NULL DEFAULT '',
    proof_count   INTEGER NOT NULL DEFAULT 0,
    source        TEXT NOT NULL DEFAULT 'heuristic',
    active        INTEGER NOT NULL DEFAULT 1,
    created       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE INDEX IF NOT EXISTS observations_project_active ON observations(project, active);

CREATE TABLE IF NOT EXISTS mental_models (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    project           TEXT NOT NULL,
    question          TEXT NOT NULL,
    answer            TEXT NOT NULL DEFAULT '',
    source_ids        TEXT NOT NULL DEFAULT '[]',
    built_from_max_id INTEGER NOT NULL DEFAULT 0,
    source            TEXT NOT NULL DEFAULT '',
    created           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE INDEX IF NOT EXISTS mental_models_project ON mental_models(project);
`

// sourceUser 未指定來源時的預設值（使用者／agent 手動寫入）。
const sourceUser = "user"

// sourceOrUser 空字串視為 user，讓舊版 export 與一般 Store 都落在預設來源。
func sourceOrUser(s string) string {
	if s == "" {
		return sourceUser
	}
	return s
}

const timeLayout = "2006-01-02T15:04:05"

// Open 開啟或建立 SQLite 資料庫，自動建立目錄與 schema。
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// WAL 允許併發讀 + 單一寫（寫不擋讀），busy_timeout 讓多個 session 的
	// serve process 遇到寫鎖時等待而非立即回報 readonly/busy。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// 舊 DB 切成 WAL 需要獨占鎖，且不會套用 busy_timeout：多個 process 同時開啟時落敗者會立刻 SQLITE_BUSY。
	// WAL 設定寫進檔案、由勝出者完成，所以落敗者忽略 BUSY 即可（其餘操作仍依 busy_timeout 等待）。
	if _, err := sqlDB.Exec(`PRAGMA journal_mode=WAL`); err != nil && !strings.Contains(err.Error(), "SQLITE_BUSY") {
		sqlDB.Close()
		return nil, fmt.Errorf("set wal: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// migrate 建 schema 並補舊 DB 缺的欄位。整段包在 BEGIN IMMEDIATE：多個 process 同時開啟時，
// 先取得寫鎖者執行，其他的依 busy_timeout 排隊；沒有它，讀鎖升級寫鎖的衝突會立刻回 SQLITE_BUSY。
func migrate(sqlDB *sql.DB) error {
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, schema); err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if err := addColumnIfMissing(conn, "memories", "source", `TEXT NOT NULL DEFAULT 'user'`); err != nil {
		conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// execQuerier *sql.DB 與 *sql.Conn 共有的查詢／執行方法，讓 migration helper 兩者都能用。
type execQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// hasColumn 以 PRAGMA table_info 檢查 table 是否已有 column。
func hasColumn(sqlDB execQuerier, table, column string) (bool, error) {
	rows, err := sqlDB.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("table info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("table info %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("table info %s: %w", table, err)
	}
	return false, nil
}

// addColumnIfMissing 舊 DB 的 table 缺少 column 時以 ALTER TABLE 補上；既有資料列取得 DEFAULT 值。
func addColumnIfMissing(sqlDB execQuerier, table, column, def string) error {
	ok, err := hasColumn(sqlDB, table, column)
	if err != nil || ok {
		return err
	}
	return alterAddColumn(sqlDB, table, column, def)
}

// alterAddColumn 執行 ALTER TABLE ADD COLUMN。多個 process 同時開啟舊 DB（例如升級後 hooks 並行）時，
// 檢查與 ALTER 之間可能被別人搶先補上而得到 duplicate column；此時重查，欄位已存在就視為成功。
func alterAddColumn(sqlDB execQuerier, table, column, def string) error {
	_, err := sqlDB.ExecContext(context.Background(), fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, def))
	if err == nil {
		return nil
	}
	if ok, cerr := hasColumn(sqlDB, table, column); cerr == nil && ok {
		return nil
	}
	return fmt.Errorf("add column %s.%s: %w", table, column, err)
}

// Close 關閉資料庫連線。
func (d *DB) Close() error {
	return d.db.Close()
}

// SetEmbedder 設定用於語意搜尋的 embedder；不設定（nil）則只使用 FTS5。
func (d *DB) SetEmbedder(e embed.Embedder) {
	d.embedder = e
}

// tryEmbed 嘗試計算並儲存一筆記憶的語意向量。embedder 未設定、呼叫逾時，或
// 寫入失敗都視為可忽略的降級情況（純 FTS5 仍可正常使用），只印警告、不回傳
// 錯誤給呼叫端。
func (d *DB) tryEmbed(id int64, content string) {
	if d.embedder == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	vec, err := d.embedder.Embed(ctx, content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory-mcp: embed skipped for #%d: %v\n", id, err)
		return
	}

	if err := d.upsertEmbedding(id, vec, d.embedder.Model()); err != nil {
		fmt.Fprintf(os.Stderr, "memory-mcp: embed store failed for #%d: %v\n", id, err)
	}
}

// upsertEmbedding 寫入或覆蓋一筆記憶的向量；Task 5 的 Reindex 會重用這個方法。
func (d *DB) upsertEmbedding(memoryID int64, vec []float32, model string) error {
	_, err := d.db.Exec(
		`INSERT INTO memory_embeddings (memory_id, vector, model) VALUES (?, ?, ?)
		 ON CONFLICT(memory_id) DO UPDATE SET vector = excluded.vector, model = excluded.model,
		     created = strftime('%Y-%m-%dT%H:%M:%S','now')`,
		memoryID, encodeVector(vec), model,
	)
	return err
}

// Store 儲存一筆記憶（寫入前先 Sanitize，mem.Content／mem.Redacted 會被回寫），回傳 auto-increment ID。
// mem.Supersedes 有值時改走 storeReplacing：舊記憶全部存在才寫入，並在同一個 transaction 內刪掉。
func (d *DB) Store(mem *Memory) (int64, error) {
	if len(mem.Supersedes) > 0 {
		return d.storeReplacing(mem, "", mem.Supersedes)
	}
	clean, n := Sanitize(mem.Content)
	if clean == "" {
		return 0, ErrEmptyAfterSanitize
	}
	mem.Content = clean
	mem.Redacted = n
	res, err := d.db.Exec(
		`INSERT INTO memories (type, content, tags, project, source) VALUES (?, ?, ?, ?, ?)`,
		mem.Type, mem.Content, mem.Tags, mem.Project, sourceOrUser(mem.Source),
	)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	d.tryEmbed(id, mem.Content)
	return id, nil
}

// Get 依 ID 取得一筆記憶。
func (d *DB) Get(id int64) (*Memory, error) {
	var m Memory
	var created, updated string
	err := d.db.QueryRow(
		selectMemoryCols+` WHERE id = ?`, id,
	).Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated, &m.Source)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	m.Created, _ = time.Parse(timeLayout, created)
	m.Updated, _ = time.Parse(timeLayout, updated)
	return &m, nil
}

const selectMemoryCols = `SELECT id, type, content, tags, project, created, updated, source FROM memories`

// queryMemories 執行 SELECT selectMemoryCols 查詢並掃描成 []Memory。
func (d *DB) queryMemories(query string, args ...any) ([]Memory, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated, &m.Source); err != nil {
			return nil, err
		}
		m.Created, _ = time.Parse(timeLayout, created)
		m.Updated, _ = time.Parse(timeLayout, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMany 依傳入順序取得多筆記憶，找不到的 ID 略過。
func (d *DB) GetMany(ids []int64) ([]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	found, err := d.queryMemories(selectMemoryCols+` WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("get many: %w", err)
	}
	byID := make(map[int64]Memory, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	var out []Memory
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

const timelineMax = 10

func clampTimeline(n int) int {
	return min(max(n, 0), timelineMax)
}

// Timeline 回傳同 project 依時間排序、以 id 為中心的前後記憶（含中心，舊到新）。
func (d *DB) Timeline(id int64, before, after int) ([]Memory, error) {
	center, err := d.Get(id)
	if err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	created := center.Created.Format(timeLayout)

	prev, err := d.queryMemories(selectMemoryCols+
		` WHERE project = ? AND (created < ? OR (created = ? AND id < ?)) ORDER BY created DESC, id DESC LIMIT ?`,
		center.Project, created, created, id, clampTimeline(before))
	if err != nil {
		return nil, fmt.Errorf("timeline before: %w", err)
	}
	next, err := d.queryMemories(selectMemoryCols+
		` WHERE project = ? AND (created > ? OR (created = ? AND id > ?)) ORDER BY created ASC, id ASC LIMIT ?`,
		center.Project, created, created, id, clampTimeline(after))
	if err != nil {
		return nil, fmt.Errorf("timeline after: %w", err)
	}

	out := make([]Memory, 0, len(prev)+1+len(next))
	for i := len(prev) - 1; i >= 0; i-- {
		out = append(out, prev[i])
	}
	out = append(out, *center)
	return append(out, next...), nil
}

// Update 更新指定記憶的內容（寫入前先 Sanitize）。
func (d *DB) Update(id int64, content string) error {
	content, _ = Sanitize(content)
	if content == "" {
		return ErrEmptyAfterSanitize
	}
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
	d.tryEmbed(id, content)
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
	if n <= 0 {
		return 0, fmt.Errorf("invalid since %q: duration must be positive", s)
	}
	return n, nil
}

// List 列出記憶，支援 type 過濾、since 時間範圍、limit 筆數。
func (d *DB) List(opts ListOptions) ([]Memory, error) {
	query := selectMemoryCols
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
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated, &m.Source); err != nil {
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

// ContextOptions 摘要查詢參數。Project 有值且 Type 為空時輸出專案分段索引。
type ContextOptions struct {
	Type    string
	Project string
	Limit   int
	Full    bool
}

const (
	contextSnippetRunes   = 120
	contextBudgetRunes    = 6000
	personalProjectPrefix = "personal-"
	homeProject           = "home"
	contextFooter         = "全文用 memory_get(ids)，前後脈絡用 memory_timeline(id)。"
)

// Context 產生 bounded 的記憶摘要。
func (d *DB) Context(opts ContextOptions) (string, error) {
	if opts.Project != "" && opts.Type == "" {
		return d.projectContext(opts.Project)
	}

	query := selectMemoryCols
	var args []any
	var where []string
	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		where = append(where, "project = ?")
		args = append(args, opts.Project)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	query += ` ORDER BY created DESC, id DESC LIMIT ?`
	args = append(args, limit)

	memories, err := d.queryMemories(query, args...)
	if err != nil {
		return "", fmt.Errorf("context: %w", err)
	}
	if len(memories) == 0 {
		return "No memories stored yet.", nil
	}
	if opts.Full {
		return fullContext(memories), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Memories (%d entries)\n\n", len(memories))
	for _, m := range memories {
		b.WriteString(CompactLine(m, contextSnippetRunes, true))
		b.WriteByte('\n')
	}
	return withFooter(b.String()), nil
}

// fullContext 舊版依 type 分組的全文格式。
func fullContext(memories []Memory) string {
	grouped := make(map[string][]string)
	var typeOrder []string
	for _, m := range memories {
		line := fmt.Sprintf("- #%d %s", m.ID, m.Content)
		if m.Tags != "" {
			line += fmt.Sprintf(" [%s]", m.Tags)
		}
		if _, ok := grouped[m.Type]; !ok {
			typeOrder = append(typeOrder, m.Type)
		}
		grouped[m.Type] = append(grouped[m.Type], line)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Memories (%d entries)\n\n", len(memories))
	for _, typ := range typeOrder {
		items := grouped[typ]
		fmt.Fprintf(&b, "### %s (%d)\n", typ, len(items))
		for _, item := range items {
			b.WriteString(item)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type contextSection struct {
	title string
	lines []string
}

// projectContext 專案索引，段落依序為：全域 observations、專案 observations、全域 feedback、
// 專案 mental models、專案 feedback、專案 summary、專案其他記憶。
// 段落順序即裁切優先序（fitBudget 從最後一段裁起）：其他記憶最先被裁，接著 summary、專案 feedback，
// 再來才是 mental models，observations 與全域 feedback 最後才被裁。mental model 是整理過的答案，
// 所以排在原始 summary 與 feedback 之前；只列出已 build（answer 非空）的 model。
// personal- 開頭的 project 是個人記憶，home 是家目錄的雜項 session，兩者都不注入專案段。
// active observation 的證據記憶不再以原始行出現；各段查詢多取「被隱藏筆數」再裁回原上限，
// 讓隱藏的證據不佔名額。沒有 active observation 與已 build 的 mental model 時，輸出與加入兩者之前逐 byte 相同。
func (d *DB) projectContext(project string) (string, error) {
	includeProject := !strings.HasPrefix(project, personalProjectPrefix) && project != homeProject
	globalObs, err := d.contextObservations("global", 10)
	if err != nil {
		return "", fmt.Errorf("context global observations: %w", err)
	}
	var projectObs []Observation
	if includeProject && project != "global" {
		projectObs, err = d.contextObservations(project, 20)
		if err != nil {
			return "", fmt.Errorf("context observations: %w", err)
		}
	}
	seen := make(map[int64]bool)
	for _, o := range append(slices.Clone(globalObs), projectObs...) {
		for _, id := range o.Evidence {
			seen[id] = true
		}
	}
	extra := len(seen)

	global, err := d.queryMemories(selectMemoryCols+
		` WHERE type = 'feedback' AND project = 'global' ORDER BY created DESC, id DESC LIMIT ?`, 10+extra)
	if err != nil {
		return "", fmt.Errorf("context global: %w", err)
	}
	var feedback, summaries, others []Memory
	var mentals []MentalModel
	if includeProject {
		mentals, err = d.contextMentalModels(project, 10)
		if err != nil {
			return "", fmt.Errorf("context mental models: %w", err)
		}
		feedback, err = d.queryMemories(selectMemoryCols+
			` WHERE type = 'feedback' AND project = ? ORDER BY created DESC, id DESC LIMIT ?`, project, 20+extra)
		if err != nil {
			return "", fmt.Errorf("context feedback: %w", err)
		}
		summaries, err = d.queryMemories(selectMemoryCols+
			` WHERE type = 'summary' AND project = ? ORDER BY created DESC, id DESC LIMIT ?`, project, 3+extra)
		if err != nil {
			return "", fmt.Errorf("context summary: %w", err)
		}
		others, err = d.queryMemories(selectMemoryCols+
			` WHERE type IN ('til', 'knowledge') AND project = ? ORDER BY created DESC, id DESC LIMIT ?`, project, 10+extra)
		if err != nil {
			return "", fmt.Errorf("context others: %w", err)
		}
	}

	toLines := func(ms []Memory, limit int) []string {
		var lines []string
		for _, m := range ms {
			if seen[m.ID] || len(lines) >= limit {
				continue
			}
			seen[m.ID] = true
			lines = append(lines, CompactLine(m, contextSnippetRunes, false))
		}
		return lines
	}
	obsLines := func(obs []Observation) []string {
		var lines []string
		for _, o := range obs {
			lines = append(lines, ObservationLine(o, contextSnippetRunes))
		}
		return lines
	}
	sections := []contextSection{
		{"### 全域 observations", obsLines(globalObs)},
		{fmt.Sprintf("### %s observations", project), obsLines(projectObs)},
		{"### 全域 feedback", toLines(global, 10)},
		{fmt.Sprintf("### %s mental models", project), mentalModelLines(mentals, contextSnippetRunes)},
		{fmt.Sprintf("### %s feedback", project), toLines(feedback, 20)},
		{fmt.Sprintf("### %s summary", project), toLines(summaries, 3)},
		{fmt.Sprintf("### %s 其他記憶", project), toLines(others, 10)},
	}

	header := fmt.Sprintf("## Memory context（project: %s）\n\n", project)
	if renderSections(header, sections) == header {
		return "", nil
	}
	return withFooter(fitBudget(header, sections, contextBudgetRunes)), nil
}

// fitBudget 從最後一段尾端逐行刪除，直到內容不超過 budget rune。
func fitBudget(header string, sections []contextSection, budget int) string {
	body := renderSections(header, sections)
	for utf8.RuneCountInString(body) > budget && dropLastLine(sections) {
		body = renderSections(header, sections)
	}
	return body
}

func renderSections(header string, sections []contextSection) string {
	var b strings.Builder
	b.WriteString(header)
	for _, s := range sections {
		if len(s.lines) == 0 {
			continue
		}
		b.WriteString(s.title + "\n")
		for _, l := range s.lines {
			b.WriteString(l + "\n")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// dropLastLine 從最後一個非空段落刪一行，沒有可刪時回 false。
func dropLastLine(sections []contextSection) bool {
	for i := len(sections) - 1; i >= 0; i-- {
		if n := len(sections[i].lines); n > 0 {
			sections[i].lines = sections[i].lines[:n-1]
			return true
		}
	}
	return false
}

func withFooter(body string) string {
	s := body + contextFooter + "\n"
	return s + fmt.Sprintf("(約 %d tokens)\n", EstimateTokens(s))
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

	stmt, err := tx.Prepare(`INSERT INTO memories (type, content, tags, project, source) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("import prepare: %w", err)
	}
	defer stmt.Close()

	var count int
	for _, m := range memories {
		if _, err := stmt.Exec(m.Type, m.Content, m.Tags, m.Project, sourceOrUser(m.Source)); err != nil {
			return count, fmt.Errorf("import row: %w", err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("import commit: %w", err)
	}
	return count, nil
}
