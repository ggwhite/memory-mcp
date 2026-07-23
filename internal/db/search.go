package db

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// SearchOptions 搜索查詢參數。
type SearchOptions struct {
	Query string
	Type  string
	Limit int // default 5
}

// SearchResult 搜索結果，包含 FTS5 排名分數。混合搜尋（embedder 已設定）開
// 啟時，Rank 只在該筆同時是 FTS5 命中時有意義；純語意命中的筆數 Rank 為 0。
type SearchResult struct {
	Memory
	Rank float64 `json:"rank"`
}

// rrfK 是 Reciprocal Rank Fusion 的平滑常數，業界慣用值。
const rrfK = 60

// ftsCandidatePoolMultiplier、ftsCandidatePoolMin 用來擴大「進入融合前」的
// FTS5 候選池大小。vectorRank 對向量端本來就是全量掃描、不做截斷，若 FTS5
// 端只給 opts.Limit（預設 5）筆候選，會讓排名恰好落在 limit 之外、但其實
// 也命中關鍵字的強語意結果在 RRF 融合時完全拿不到 FTS5 名次分數，等於白
// 白浪費了雙路排序融合的意義。這裡把 FTS5 候選池放大到與向量端相當的深
// 度，讓兩邊在融合時有可比較的候選規模；最終輸出仍會在融合「之後」依
// opts.Limit 截斷（見 Search 內 fused 的截斷邏輯，不受此常數影響）。
const (
	ftsCandidatePoolMultiplier = 4
	ftsCandidatePoolMin        = 20
)

// Search 混合 FTS5 全文搜索與語意向量搜索（embedder 已設定時），用
// Reciprocal Rank Fusion 合併排序；embedder 未設定或呼叫失敗時自動降級為
// 純 FTS5 結果。
func (d *DB) Search(opts SearchOptions) ([]SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 5
	}

	ftsResults, err := d.ftsSearch(opts, limit)
	if err != nil {
		return nil, err
	}
	if d.embedder == nil {
		return ftsResults, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	queryVec, err := d.embedder.Embed(ctx, opts.Query)
	if err != nil {
		return ftsResults, nil
	}

	vectorIDs, err := d.vectorRank(queryVec, opts.Type)
	if err != nil || len(vectorIDs) == 0 {
		return ftsResults, nil
	}

	// 融合用的 FTS5 候選池比最終輸出的 limit 寬，好讓 FTS5 排名跟向量排名有
	// 相當深度可以融合；真正回傳給呼叫端的筆數仍由下面的截斷邏輯決定。
	ftsCandidateLimit := limit * ftsCandidatePoolMultiplier
	if ftsCandidateLimit < ftsCandidatePoolMin {
		ftsCandidateLimit = ftsCandidatePoolMin
	}
	ftsCandidates, err := d.ftsSearch(opts, ftsCandidateLimit)
	if err != nil {
		return nil, err
	}

	ftsIDs := make([]int64, len(ftsCandidates))
	for i, r := range ftsCandidates {
		ftsIDs[i] = r.ID
	}

	fused := reciprocalRankFusion(ftsIDs, vectorIDs, rrfK)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return d.hydrateResults(fused, ftsCandidates)
}

// ftsSearch 使用 FTS5 全文搜索記憶，依相關度排序。
func (d *DB) ftsSearch(opts SearchOptions, limit int) ([]SearchResult, error) {
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

	query += ` ORDER BY f.rank LIMIT ?`
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

// vectorRank 計算 queryVec 與所有（依 type 篩選後）記憶向量的 cosine
// similarity，回傳依相似度由高到低排序的記憶 ID 清單。目前記憶量規模
// （~220 筆）brute-force 全量計算即可，不做提前截斷。
func (d *DB) vectorRank(queryVec []float32, typ string) ([]int64, error) {
	query := `SELECT e.memory_id, e.vector FROM memory_embeddings e`
	var args []any
	if typ != "" {
		query += ` JOIN memories m ON m.id = e.memory_id WHERE m.type = ?`
		args = append(args, typ)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector rank: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id    int64
		score float64
	}
	var scoredList []scored
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("vector rank scan: %w", err)
		}
		scoredList = append(scoredList, scored{id: id, score: cosineSimilarity(queryVec, decodeVector(blob))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(scoredList, func(i, j int) bool {
		return scoredList[i].score > scoredList[j].score
	})

	ids := make([]int64, len(scoredList))
	for i, s := range scoredList {
		ids[i] = s.id
	}
	return ids, nil
}

// hydrateResults 依 fusedIDs 順序組出最終結果，優先重用 ftsResults 裡已經
// 查過的資料，純語意命中（不在 ftsResults 裡）的才額外查一次。
func (d *DB) hydrateResults(fusedIDs []int64, ftsResults []SearchResult) ([]SearchResult, error) {
	byID := make(map[int64]SearchResult, len(ftsResults))
	for _, r := range ftsResults {
		byID[r.ID] = r
	}

	out := make([]SearchResult, 0, len(fusedIDs))
	for _, id := range fusedIDs {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			continue
		}
		m, err := d.Get(id)
		if err != nil {
			continue // 記憶可能剛好被刪除，略過
		}
		out = append(out, SearchResult{Memory: *m})
	}
	return out, nil
}
