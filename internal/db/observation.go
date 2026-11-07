package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Observation 由多筆近似重複的記憶（evidence）整併出的一句規則；active = false 表示已被使用者停用，
// 停用後證據記憶重新出現在 context。證據記憶本身永遠不刪除。
type Observation struct {
	ID           int64     `json:"id"`
	Project      string    `json:"project"`
	Type         string    `json:"type"`
	Content      string    `json:"content"`
	Evidence     []int64   `json:"evidence"`
	EvidenceHash string    `json:"evidence_hash"`
	ProofCount   int       `json:"proof_count"`
	Source       string    `json:"source"`
	Active       bool      `json:"active"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}

// ObservationFilter ListObservations 的查詢條件；AllProjects 為 true 時忽略 Project，Type 空字串表示不限。
type ObservationFilter struct {
	Project         string
	AllProjects     bool
	Type            string
	IncludeInactive bool
}

// Candidate consolidate 的候選記憶，附上已儲存的向量（沒有則 Vector 為 nil）。
type Candidate struct {
	Memory
	Vector []float32
	Model  string
}

// EvidenceHash 回傳證據 id 集合的雜湊，與順序、重複無關；用來判斷整併結果是否有變。
func EvidenceHash(ids []int64) string {
	sorted := sortedUniqueIDs(ids)
	parts := make([]string, len(sorted))
	for i, id := range sorted {
		parts[i] = strconv.FormatInt(id, 10)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

func sortedUniqueIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

const selectObservationCols = `SELECT id, project, type, content, evidence, evidence_hash, proof_count, source, active, created, updated FROM observations`

func (d *DB) queryObservations(query string, args ...any) ([]Observation, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		var o Observation
		var evidence, created, updated string
		if err := rows.Scan(&o.ID, &o.Project, &o.Type, &o.Content, &evidence, &o.EvidenceHash, &o.ProofCount, &o.Source, &o.Active, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidence), &o.Evidence); err != nil {
			return nil, fmt.Errorf("observation o%d evidence: %w", o.ID, err)
		}
		o.Created, _ = time.Parse(timeLayout, created)
		o.Updated, _ = time.Parse(timeLayout, updated)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListObservations 依條件列出 observations，依 project、id 排序；預設只列 active。
func (d *DB) ListObservations(f ObservationFilter) ([]Observation, error) {
	var where []string
	var args []any
	if !f.AllProjects {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.Type != "" {
		where = append(where, "type = ?")
		args = append(args, f.Type)
	}
	if !f.IncludeInactive {
		where = append(where, "active = 1")
	}
	query := selectObservationCols
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	out, err := d.queryObservations(query+" ORDER BY project, id", args...)
	if err != nil {
		return nil, fmt.Errorf("list observations: %w", err)
	}
	return out, nil
}

// SaveObservations 在單一 transaction 內寫入 observations：ID 為 0 新增，否則更新該筆 active observation
// 的內容、證據與來源。Evidence 會排序去重，ProofCount 與 EvidenceHash 依 Evidence 重算，Content 先 Sanitize。
// 回傳每筆的 id（與輸入同順序）。
func (d *DB) SaveObservations(obs []Observation) ([]int64, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("save observations begin: %w", err)
	}
	defer tx.Rollback()

	ids := make([]int64, len(obs))
	for i, o := range obs {
		content, _ := Sanitize(o.Content)
		if content == "" {
			return nil, ErrEmptyAfterSanitize
		}
		evidence := sortedUniqueIDs(o.Evidence)
		data, err := json.Marshal(evidence)
		if err != nil {
			return nil, fmt.Errorf("save observations: %w", err)
		}
		if o.ID == 0 {
			res, err := tx.Exec(
				`INSERT INTO observations (project, type, content, evidence, evidence_hash, proof_count, source) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				o.Project, o.Type, content, string(data), EvidenceHash(evidence), len(evidence), o.Source,
			)
			if err != nil {
				return nil, fmt.Errorf("insert observation: %w", err)
			}
			if ids[i], err = res.LastInsertId(); err != nil {
				return nil, fmt.Errorf("insert observation: %w", err)
			}
			continue
		}
		res, err := tx.Exec(
			`UPDATE observations SET content = ?, evidence = ?, evidence_hash = ?, proof_count = ?, source = ?,
			     updated = strftime('%Y-%m-%dT%H:%M:%S','now')
			 WHERE id = ? AND active = 1`,
			content, string(data), EvidenceHash(evidence), len(evidence), o.Source, o.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("update observation o%d: %w", o.ID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, fmt.Errorf("update observation: o%d not found or inactive", o.ID)
		}
		ids[i] = o.ID
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("save observations commit: %w", err)
	}
	return ids, nil
}

// DeactivateObservation 停用一筆 active observation（active = 0），其證據記憶重新出現在 context。
func (d *DB) DeactivateObservation(id int64) error {
	res, err := d.db.Exec(
		`UPDATE observations SET active = 0, updated = strftime('%Y-%m-%dT%H:%M:%S','now') WHERE id = ? AND active = 1`, id)
	if err != nil {
		return fmt.Errorf("deactivate observation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deactivate observation: o%d not found or already inactive", id)
	}
	return nil
}

// ConsolidationCandidates 列出 (project, type) 的所有記憶與其已儲存向量，依 created、id 由舊到新。
func (d *DB) ConsolidationCandidates(project, typ string) ([]Candidate, error) {
	rows, err := d.db.Query(
		`SELECT m.id, m.type, m.content, m.tags, m.project, m.created, m.updated, m.source, e.vector, e.model
		 FROM memories m LEFT JOIN memory_embeddings e ON e.memory_id = m.id
		 WHERE m.project = ? AND m.type = ? ORDER BY m.created, m.id`, project, typ)
	if err != nil {
		return nil, fmt.Errorf("consolidation candidates: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var created, updated string
		var vector []byte
		var model *string
		if err := rows.Scan(&c.ID, &c.Type, &c.Content, &c.Tags, &c.Project, &created, &updated, &c.Source, &vector, &model); err != nil {
			return nil, fmt.Errorf("consolidation candidates scan: %w", err)
		}
		c.Created, _ = time.Parse(timeLayout, created)
		c.Updated, _ = time.Parse(timeLayout, updated)
		if vector != nil {
			c.Vector = decodeVector(vector)
		}
		if model != nil {
			c.Model = *model
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MemoryProjects 列出有 typ 類型記憶的所有 project（含空字串），依名稱排序。
func (d *DB) MemoryProjects(typ string) ([]string, error) {
	rows, err := d.db.Query(`SELECT DISTINCT project FROM memories WHERE type = ? ORDER BY project`, typ)
	if err != nil {
		return nil, fmt.Errorf("memory projects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("memory projects scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// contextObservations 取得 project 的 active observations 供 context 使用，證據多的優先。
func (d *DB) contextObservations(project string, limit int) ([]Observation, error) {
	return d.queryObservations(selectObservationCols+
		` WHERE project = ? AND active = 1 ORDER BY proof_count DESC, id DESC LIMIT ?`, project, limit)
}
