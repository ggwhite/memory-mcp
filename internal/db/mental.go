package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MentalModel 使用者對某個 project 長期關注的問題，由 LLM 根據記憶整理出答案（mental build）。
// Answer 為空表示尚未 build；BuiltFromMaxID 是 build 當下該 project 最大的記憶 id，用來判斷是否需要重建。
type MentalModel struct {
	ID             int64     `json:"id"`
	Project        string    `json:"project"`
	Question       string    `json:"question"`
	Answer         string    `json:"answer"`
	SourceIDs      []int64   `json:"source_ids"`
	BuiltFromMaxID int64     `json:"built_from_max_id"`
	Source         string    `json:"source"`
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
}

// MentalModelFilter ListMentalModels 的查詢條件；AllProjects 為 true 時忽略 Project。
type MentalModelFilter struct {
	Project     string
	AllProjects bool
}

const selectMentalModelCols = `SELECT id, project, question, answer, source_ids, built_from_max_id, source, created, updated FROM mental_models`

func (d *DB) queryMentalModels(query string, args ...any) ([]MentalModel, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MentalModel
	for rows.Next() {
		var m MentalModel
		var sourceIDs, created, updated string
		if err := rows.Scan(&m.ID, &m.Project, &m.Question, &m.Answer, &sourceIDs, &m.BuiltFromMaxID, &m.Source, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sourceIDs), &m.SourceIDs); err != nil {
			return nil, fmt.Errorf("mental model m%d source_ids: %w", m.ID, err)
		}
		m.Created, _ = time.Parse(timeLayout, created)
		m.Updated, _ = time.Parse(timeLayout, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMentalModel 為 project 新增一個待 build 的問題；project 與 question（Sanitize 後）都不可為空。
func (d *DB) AddMentalModel(project, question string) (int64, error) {
	if strings.TrimSpace(project) == "" {
		return 0, errors.New("add mental model: project is required")
	}
	question, _ = Sanitize(question)
	question = strings.TrimSpace(question)
	if question == "" {
		return 0, errors.New("add mental model: question is empty")
	}
	res, err := d.db.Exec(`INSERT INTO mental_models (project, question) VALUES (?, ?)`, project, question)
	if err != nil {
		return 0, fmt.Errorf("add mental model: %w", err)
	}
	return res.LastInsertId()
}

// ListMentalModels 依條件列出 mental models，依 project、id 排序。
func (d *DB) ListMentalModels(f MentalModelFilter) ([]MentalModel, error) {
	query := selectMentalModelCols
	var args []any
	if !f.AllProjects {
		query += " WHERE project = ?"
		args = append(args, f.Project)
	}
	out, err := d.queryMentalModels(query+" ORDER BY project, id", args...)
	if err != nil {
		return nil, fmt.Errorf("list mental models: %w", err)
	}
	return out, nil
}

// SaveMentalModelAnswer 寫入 m.ID 的 build 結果（answer、source_ids、built_from_max_id、source）並更新 updated；
// Answer 先 Sanitize，SourceIDs 排序去重。該 id 不存在時回傳 error。
func (d *DB) SaveMentalModelAnswer(m MentalModel) error {
	answer, _ := Sanitize(m.Answer)
	data, err := json.Marshal(sortedUniqueIDs(m.SourceIDs))
	if err != nil {
		return fmt.Errorf("save mental model: %w", err)
	}
	res, err := d.db.Exec(
		`UPDATE mental_models SET answer = ?, source_ids = ?, built_from_max_id = ?, source = ?,
		     updated = strftime('%Y-%m-%dT%H:%M:%S','now')
		 WHERE id = ?`,
		answer, string(data), m.BuiltFromMaxID, m.Source, m.ID,
	)
	if err != nil {
		return fmt.Errorf("save mental model m%d: %w", m.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("save mental model: m%d not found", m.ID)
	}
	return nil
}

// DeleteMentalModel 刪除一個 mental model（問題與答案都是衍生資料，不保留）。
func (d *DB) DeleteMentalModel(id int64) error {
	res, err := d.db.Exec(`DELETE FROM mental_models WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mental model: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("delete mental model: m%d not found", id)
	}
	return nil
}

// MentalModelProjects 列出有 mental model 的所有 project，依名稱排序。
func (d *DB) MentalModelProjects() ([]string, error) {
	rows, err := d.db.Query(`SELECT DISTINCT project FROM mental_models ORDER BY project`)
	if err != nil {
		return nil, fmt.Errorf("mental model projects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("mental model projects scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MaxMemoryID 回傳 project 最大的記憶 id，沒有記憶時回 0。
func (d *DB) MaxMemoryID(project string) (int64, error) {
	var n int64
	if err := d.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM memories WHERE project = ?`, project).Scan(&n); err != nil {
		return 0, fmt.Errorf("max memory id: %w", err)
	}
	return n, nil
}

// RecentMemories 回傳 project 中 typ 類型最新的 limit 筆記憶，由新到舊。
func (d *DB) RecentMemories(project, typ string, limit int) ([]Memory, error) {
	out, err := d.queryMemories(selectMemoryCols+
		` WHERE project = ? AND type = ? ORDER BY created DESC, id DESC LIMIT ?`, project, typ, limit)
	if err != nil {
		return nil, fmt.Errorf("recent memories: %w", err)
	}
	return out, nil
}

// contextMentalModels 取得 project 已 build（answer 非空）的 mental models 供 context 使用，依 id 排序。
func (d *DB) contextMentalModels(project string, limit int) ([]MentalModel, error) {
	return d.queryMentalModels(selectMentalModelCols+
		` WHERE project = ? AND answer != '' ORDER BY id LIMIT ?`, project, limit)
}

// mentalModelLines context 用的行：「**Q:** 問題」接著答案的每個非空行，各行依 n 裁切。
func mentalModelLines(ms []MentalModel, n int) []string {
	var lines []string
	for _, m := range ms {
		lines = append(lines, "**Q:** "+Snippet(m.Question, n))
		for _, l := range strings.Split(m.Answer, "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			lines = append(lines, Snippet(l, n))
		}
	}
	return lines
}
