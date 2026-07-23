# 語意向量搜尋 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在現有 SQLite FTS5 關鍵字搜尋之外，加入透過本機 Ollama 產生的語意向量搜尋，`memory_search`／`memory-mcp search` 用 Reciprocal Rank Fusion 混合兩種排序，Ollama 不在線時自動降級為純 FTS5，不中斷現有功能。

**Architecture:** 新增 `internal/embed` package 封裝 `Embedder` interface 與呼叫本機 Ollama HTTP API 的 `OllamaEmbedder`。`internal/db` 新增 `memory_embeddings` 表與一組純函式（向量編碼/解碼、cosine similarity、RRF 融合），`DB.Store`/`DB.Update` 在有設定 embedder 時同步計算並寫入向量，`DB.Search` 混合 FTS5 與向量排序。新增 `memory-mcp reindex` 指令補算既有資料或 Ollama 曾離線時漏算的向量。`Store` interface 簽章、`httpapi` REST 介面完全不變——向量邏輯全部封裝在中央機器（跑實體 `*db.DB` 那台）內部，`--remote` 跨機器同步不受影響、不需要改動。

**Tech Stack:** Go、`modernc.org/sqlite`（既有，純 Go 無 CGO）、標準函式庫 `net/http`/`encoding/json`（呼叫 Ollama，不新增 Go module 依賴）

## Global Constraints

- Pure Go SQLite：`modernc.org/sqlite`，不可用 CGO、不可用 `sqlite-vec` 等 C extension
- 語意向量搜尋不新增任何 go.mod 依賴（Ollama 呼叫用標準函式庫 `net/http`/`encoding/json`）
- Embedding 只在「中央機器」（跑實體 `*db.DB`）發生；`httpapi.Client`（`--remote`）完全不需要改動、不需要 Ollama
- Ollama 不在線或呼叫失敗一律優雅降級（`Store`/`Update` 照常完成、`Search` 退回純 FTS5），不得回傳錯誤中斷既有功能
- 設定走環境變數，皆有預設值，不加新的必填 CLI flag：`MEMORY_MCP_OLLAMA_URL`（預設 `http://localhost:11434`）、`MEMORY_MCP_EMBED_MODEL`（預設 `nomic-embed-text`）
- GoDoc on exported identifiers，comment 一律繁體中文
- `go vet ./...` clean
- 目前記憶量 ~220 筆：向量相似度用 Go brute-force 全量計算，不做 top-N 提前截斷、不引入額外索引結構

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/embed/embed.go` | `Embedder` interface + `OllamaEmbedder`（呼叫本機 Ollama `/api/embed`） |
| `internal/embed/embed_test.go` | `OllamaEmbedder` 測試（`httptest` 假 Ollama：成功／錯誤狀態碼／逾時） |
| `internal/db/vector.go` | 向量編碼/解碼（float32 BLOB）、cosine similarity、Reciprocal Rank Fusion（純函式） |
| `internal/db/vector_test.go` | 向量數學函式測試 |
| `internal/db/db.go` | 修改：`memory_embeddings` schema、`foreign_keys` pragma、`DB.embedder` 欄位、`SetEmbedder`、`tryEmbed`，`Store`/`Update` 接上 embedding |
| `internal/db/embed_integration_test.go` | Store/Update/Delete 的 embedding 寫入、降級、cascade delete 測試；定義共用的 `fakeEmbedder` |
| `internal/db/search.go` | 修改：`Search` 改為 FTS5+向量混合排序，新增 `ftsSearch`/`vectorRank`/`hydrateResults` |
| `internal/db/hybrid_search_test.go` | 混合搜尋測試（純 FTS5 降級、embedder 失敗降級、純語意命中案例） |
| `internal/db/reindex.go` | `DB.Reindex` 方法 + `ReindexStats` |
| `internal/db/reindex_test.go` | Reindex 測試（補算、冪等、換模型後重算、無 embedder 時 no-op） |
| `internal/cli/commands.go` | 修改：`openDB()` 接上 embedder，新增 `reindex` 指令 |
| `README.md` | 修改：補充語意搜尋使用說明、環境變數、`reindex` 指令 |

## Task Dependencies

```
Task 1 (embed package)  ──┐
                           ├──→ Task 3 (schema + store/update wiring) ──→ Task 4 (hybrid search) ──→ Task 5 (reindex) ──→ Task 6 (CLI wiring + docs)
Task 2 (vector math)    ──┘
```

Task 1 與 Task 2 互不依賴，可任選順序（下方依序列出）。

---

### Task 1: `internal/embed` — Ollama Embedder

**Files:**
- Create: `internal/embed/embed.go`
- Test: `internal/embed/embed_test.go`

**Interfaces:**
- Produces: `type Embedder interface { Embed(ctx context.Context, text string) ([]float32, error); Model() string }`；`func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder`；`func NewOllamaEmbedderFromEnv() *OllamaEmbedder`

- [ ] **Step 1: Write the failing tests**

Create `internal/embed/embed_test.go`:

```go
package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaEmbedderSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("model = %q, want test-model", req.Model)
		}
		if req.Input != "hello world" {
			t.Fatalf("input = %q, want hello world", req.Input)
		}
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 {
		t.Fatalf("len(vec) = %d, want 3", len(vec))
	}
	if vec[0] != 0.1 {
		t.Fatalf("vec[0] = %f, want 0.1", vec[0])
	}
}

func TestOllamaEmbedderErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestOllamaEmbedderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	e.hc.Timeout = 50 * time.Millisecond
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestOllamaEmbedderModel(t *testing.T) {
	e := NewOllamaEmbedder("http://example.com", "my-model")
	if e.Model() != "my-model" {
		t.Fatalf("Model() = %q, want my-model", e.Model())
	}
}

func TestOllamaEmbedderDefaults(t *testing.T) {
	e := NewOllamaEmbedder("", "")
	if e.baseURL != "http://localhost:11434" {
		t.Fatalf("baseURL = %q, want http://localhost:11434", e.baseURL)
	}
	if e.model != "nomic-embed-text" {
		t.Fatalf("model = %q, want nomic-embed-text", e.model)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/embed/... -v`
Expected: FAIL — `embed.go` 不存在，`NewOllamaEmbedder`/`embedRequest`/`embedResponse` 未定義

- [ ] **Step 3: Write the implementation**

Create `internal/embed/embed.go`:

```go
// Package embed 提供把文字轉成向量的 Embedder，供 db package 做語意搜尋用。
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Embedder 把文字轉成向量。
type Embedder interface {
	// Embed 回傳 text 的 embedding 向量。
	Embed(ctx context.Context, text string) ([]float32, error)
	// Model 回傳目前使用的 embedding 模型名稱。
	Model() string
}

// OllamaEmbedder 呼叫本機 Ollama HTTP API（/api/embed）產生 embedding。
type OllamaEmbedder struct {
	baseURL string
	model   string
	hc      *http.Client
}

// NewOllamaEmbedder 建立 OllamaEmbedder，baseURL/model 為空字串時套用預設值。
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		hc:      &http.Client{Timeout: 3 * time.Second},
	}
}

// NewOllamaEmbedderFromEnv 依 MEMORY_MCP_OLLAMA_URL / MEMORY_MCP_EMBED_MODEL
// 環境變數建立 OllamaEmbedder，未設定時使用預設值。
func NewOllamaEmbedderFromEnv() *OllamaEmbedder {
	return NewOllamaEmbedder(os.Getenv("MEMORY_MCP_OLLAMA_URL"), os.Getenv("MEMORY_MCP_EMBED_MODEL"))
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed 見 Embedder。
func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: o.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("embed marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: unexpected status %d", resp.StatusCode)
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("embed: empty response")
	}
	return out.Embeddings[0], nil
}

// Model 見 Embedder。
func (o *OllamaEmbedder) Model() string {
	return o.model
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/embed/... -v`
Expected: PASS（5 個測試全過）

- [ ] **Step 5: Commit**

```bash
git add internal/embed/embed.go internal/embed/embed_test.go
git commit -m "feat: add Ollama embedder for semantic search"
```

---

### Task 2: 向量數學純函式（編碼/解碼、cosine similarity、RRF 融合）

**Files:**
- Create: `internal/db/vector.go`
- Test: `internal/db/vector_test.go`

**Interfaces:**
- Produces: `encodeVector(v []float32) []byte`、`decodeVector(b []byte) []float32`、`cosineSimilarity(a, b []float32) float64`、`reciprocalRankFusion(a, b []int64, rrfK float64) []int64`（皆為 `db` package 內部函式，不 export）

- [ ] **Step 1: Write the failing tests**

Create `internal/db/vector_test.go`:

```go
package db

import (
	"math"
	"testing"
)

func TestEncodeDecodeVector(t *testing.T) {
	v := []float32{0.1, -0.2, 3.5, 0}
	blob := encodeVector(v)
	if len(blob) != len(v)*4 {
		t.Fatalf("len(blob) = %d, want %d", len(blob), len(v)*4)
	}
	got := decodeVector(blob)
	if len(got) != len(v) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(v))
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("got[%d] = %f, want %f", i, got[i], v[i])
		}
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	v := []float32{1, 2, 3}
	got := cosineSimilarity(v, v)
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("cosineSimilarity(v, v) = %f, want 1.0", got)
	}
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := cosineSimilarity(a, b)
	if math.Abs(got) > 1e-9 {
		t.Fatalf("cosineSimilarity(orthogonal) = %f, want 0", got)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	a := []float32{0, 0}
	b := []float32{1, 1}
	if got := cosineSimilarity(a, b); got != 0 {
		t.Fatalf("cosineSimilarity(zero vector) = %f, want 0", got)
	}
}

func TestReciprocalRankFusionOverlap(t *testing.T) {
	// id 1 在兩個排名都名列前茅，融合後應該排第一。
	a := []int64{1, 2, 3}
	b := []int64{1, 3, 2}
	got := reciprocalRankFusion(a, b, 60)
	if got[0] != 1 {
		t.Fatalf("got[0] = %d, want 1", got[0])
	}
}

func TestReciprocalRankFusionOneEmpty(t *testing.T) {
	a := []int64{5, 6}
	got := reciprocalRankFusion(a, nil, 60)
	if len(got) != 2 || got[0] != 5 || got[1] != 6 {
		t.Fatalf("got = %v, want [5 6]", got)
	}
}

func TestReciprocalRankFusionDisjoint(t *testing.T) {
	a := []int64{1, 2}
	b := []int64{3, 4}
	got := reciprocalRankFusion(a, b, 60)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	// a、b 的第一名分數並列最高；stable sort 保留 a 先加入的順序，1 排最前。
	if got[0] != 1 {
		t.Fatalf("got[0] = %d, want 1", got[0])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db/... -run 'Vector|ReciprocalRankFusion' -v`
Expected: FAIL — `encodeVector`/`decodeVector`/`cosineSimilarity`/`reciprocalRankFusion` 未定義

- [ ] **Step 3: Write the implementation**

Create `internal/db/vector.go`:

```go
package db

import (
	"encoding/binary"
	"math"
	"sort"
)

// encodeVector 把 float32 向量編碼成 little-endian BLOB，供 SQLite 儲存。
func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector 把 BLOB 還原成 float32 向量。
func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosineSimilarity 計算兩個向量的餘弦相似度；任一向量為零向量時回傳 0。
func cosineSimilarity(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// reciprocalRankFusion 合併兩組依名次排序的 ID 清單（各自第一名代表最相關），
// 用 RRF 公式 score = Σ 1/(rrfK + rank) 計算融合分數，回傳依分數由高到低排序
// 的 ID 清單。rrfK 是平滑常數（業界慣用值 60）。分數相同時保留 a 優先、原始
// 加入順序在後（stable sort）。
func reciprocalRankFusion(a, b []int64, rrfK float64) []int64 {
	scores := make(map[int64]float64)
	var order []int64

	addRanks := func(ids []int64) {
		for i, id := range ids {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
			}
			scores[id] += 1.0 / (rrfK + float64(i+1))
		}
	}
	addRanks(a)
	addRanks(b)

	sort.SliceStable(order, func(i, j int) bool {
		return scores[order[i]] > scores[order[j]]
	})
	return order
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/db/... -run 'Vector|ReciprocalRankFusion' -v`
Expected: PASS（7 個測試全過）

- [ ] **Step 5: Commit**

```bash
git add internal/db/vector.go internal/db/vector_test.go
git commit -m "feat: add vector encoding, cosine similarity, and RRF fusion helpers"
```

---

### Task 3: Schema + `SetEmbedder` + Store/Update 接上 embedding

**Files:**
- Modify: `internal/db/db.go`
- Create: `internal/db/embed_integration_test.go`

**Interfaces:**
- Consumes: `embed.Embedder`（Task 1）、`encodeVector`（Task 2）
- Produces: `func (d *DB) SetEmbedder(e embed.Embedder)`、`func (d *DB) upsertEmbedding(memoryID int64, vec []float32, model string) error`、`memory_embeddings` 表（`memory_id`, `vector`, `model`, `created`）；`fakeEmbedder` test double（供 Task 4/5 測試重用）

- [ ] **Step 1: Write the failing tests**

Create `internal/db/embed_integration_test.go`:

```go
package db

import (
	"context"
	"fmt"
	"testing"
)

// fakeEmbedder 是測試用的 embed.Embedder 假實作，供本檔與後續 hybrid
// search／reindex 測試共用。
type fakeEmbedder struct {
	vec   []float32
	model string
	err   error
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vec, nil
}

func (f *fakeEmbedder) Model() string {
	return f.model
}

func TestStoreWithEmbedderPersistsVector(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1, 0.2}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestStoreWithoutEmbedderSkipsVector(t *testing.T) {
	d := testDB(t)

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (no embedder configured)", count)
	}
}

func TestStoreWithFailingEmbedderStillSucceeds(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{err: fmt.Errorf("ollama down")})

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatalf("Store should succeed even if embedder fails: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected valid id")
	}
}

func TestUpdateRefreshesEmbedding(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Update(id, "v2"); err != nil {
		t.Fatal(err)
	}

	var blob []byte
	if err := d.db.QueryRow(`SELECT vector FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob) != 4 { // 1 個 float32 = 4 bytes
		t.Fatalf("len(blob) = %d, want 4", len(blob))
	}
}

func TestDeleteCascadesEmbedding(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(id); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (cascade delete)", count)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db/... -run 'Embedder|CascadesEmbedding|RefreshesEmbedding' -v`
Expected: FAIL — `SetEmbedder`/`memory_embeddings` 表不存在

- [ ] **Step 3: Write the implementation**

Modify `internal/db/db.go`：

1. 加入 import：

```go
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"memory-mcp/internal/embed"

	_ "modernc.org/sqlite"
)
```

2. `DSN` 加上 `foreign_keys` pragma（`ON DELETE CASCADE` 預設不生效，需要每個連線開啟）：

```go
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
```

3. `schema` 常數新增 `memory_embeddings` 表（接在既有 `memories_au` trigger 之後）：

```go
CREATE TABLE IF NOT EXISTS memory_embeddings (
    memory_id INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector    BLOB NOT NULL,
    model     TEXT NOT NULL,
    created   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);
```

4. `DB` struct 加上 `embedder` 欄位：

```go
// DB SQLite 資料庫連線。
type DB struct {
	db       *sql.DB
	embedder embed.Embedder
}
```

5. 新增 `SetEmbedder` 與 `tryEmbed`（放在 `Close` 方法之後）：

```go
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
```

6. `Store` 結尾接上 `tryEmbed`：

```go
// Store 儲存一筆記憶，回傳 auto-increment ID。
func (d *DB) Store(mem *Memory) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO memories (type, content, tags, project) VALUES (?, ?, ?, ?)`,
		mem.Type, mem.Content, mem.Tags, mem.Project,
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
```

7. `Update` 結尾接上 `tryEmbed`：

```go
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
	d.tryEmbed(id, content)
	return nil
}
```

`Delete` 不需要修改：`ON DELETE CASCADE` 搭配步驟 2 開啟的 `foreign_keys` pragma 會自動清掉對應的 `memory_embeddings` 列。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/db/... -v`
Expected: PASS（全部既有測試 + 本次新增測試都過，含 `TestDeleteCascadesEmbedding`）

- [ ] **Step 5: Commit**

```bash
git add internal/db/db.go internal/db/embed_integration_test.go
git commit -m "feat: persist semantic embeddings on store/update, cascade delete"
```

---

### Task 4: 混合搜尋（FTS5 + 向量 + RRF）

**Files:**
- Modify: `internal/db/search.go`
- Create: `internal/db/hybrid_search_test.go`

**Interfaces:**
- Consumes: `d.embedder embed.Embedder`（Task 3）、`decodeVector`/`cosineSimilarity`/`reciprocalRankFusion`（Task 2）、`fakeEmbedder`（Task 3 的 `embed_integration_test.go`）
- Produces: `(d *DB) Search(opts SearchOptions) ([]SearchResult, error)` 行為變更（簽章不變）；內部新增 `ftsSearch`/`vectorRank`/`hydrateResults`

- [ ] **Step 1: Write the failing tests**

Create `internal/db/hybrid_search_test.go`:

```go
package db

import (
	"context"
	"fmt"
	"testing"
)

var errFakeEmbed = fmt.Errorf("ollama unreachable")

// keyedEmbedder 依內容文字回傳預先定義好的向量，用來模擬「語意相關但關鍵字
// 不重疊」的情境。
type keyedEmbedder struct {
	vectors map[string][]float32
}

func (k *keyedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if v, ok := k.vectors[text]; ok {
		return v, nil
	}
	return []float32{0, 0}, nil
}

func (k *keyedEmbedder) Model() string { return "keyed-fake" }

func TestSearchFallsBackToFTSWithoutEmbedder(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})

	results, err := d.Search(SearchOptions{Query: "database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestSearchFallsBackWhenEmbedderFails(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})
	d.SetEmbedder(&fakeEmbedder{err: errFakeEmbed})

	results, err := d.Search(SearchOptions{Query: "database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1 (fallback to FTS5)", len(results))
	}
}

func TestSearchSurfacesSemanticOnlyMatch(t *testing.T) {
	d := testDB(t)
	embedder := &keyedEmbedder{
		vectors: map[string][]float32{
			"how do I fix flaky CI":               {1, 0},
			"retry logic solved the CI flakiness": {1, 0}, // 語意相關，關鍵字不重疊
			"unrelated memory about cooking":       {0, 1},
		},
	}
	d.SetEmbedder(embedder)

	d.Store(&Memory{Type: "til", Content: "retry logic solved the CI flakiness"})
	d.Store(&Memory{Type: "til", Content: "unrelated memory about cooking"})

	results, err := d.Search(SearchOptions{Query: "how do I fix flaky CI", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.Content == "retry logic solved the CI flakiness" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected semantic match for 'retry logic solved the CI flakiness'")
	}
}

func TestSearchRespectsLimitAfterFusion(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{1, 0}, model: "fake"})
	d.Store(&Memory{Type: "til", Content: "database one"})
	d.Store(&Memory{Type: "til", Content: "database two"})
	d.Store(&Memory{Type: "til", Content: "database three"})

	results, err := d.Search(SearchOptions{Query: "database", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db/... -run 'Search' -v`
Expected: FAIL — `TestSearchSurfacesSemanticOnlyMatch` 找不到純語意命中的記憶（目前 `Search` 還沒有向量路徑）

- [ ] **Step 3: Write the implementation**

Replace `internal/db/search.go` entirely:

```go
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

	ftsIDs := make([]int64, len(ftsResults))
	for i, r := range ftsResults {
		ftsIDs[i] = r.ID
	}

	fused := reciprocalRankFusion(ftsIDs, vectorIDs, rrfK)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return d.hydrateResults(fused, ftsResults)
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/db/... -v`
Expected: PASS（全部測試，含既有的 `TestSearch`/`TestSearchTypeFilter`/`TestSearchLimit`）

- [ ] **Step 5: Commit**

```bash
git add internal/db/search.go internal/db/hybrid_search_test.go
git commit -m "feat: hybrid FTS5 + semantic vector search with RRF fusion"
```

---

### Task 5: `Reindex` — 補算既有資料的 embedding

**Files:**
- Create: `internal/db/reindex.go`
- Create: `internal/db/reindex_test.go`

**Interfaces:**
- Consumes: `d.embedder embed.Embedder`（Task 3）、`d.upsertEmbedding(memoryID int64, vec []float32, model string) error`（Task 3）、`fakeEmbedder`（Task 3）
- Produces: `type ReindexStats struct { Total, Processed, Failed int }`、`func (d *DB) Reindex(ctx context.Context) (ReindexStats, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/db/reindex_test.go`:

```go
package db

import (
	"context"
	"testing"
)

func TestReindexBackfillsMissingEmbeddings(t *testing.T) {
	d := testDB(t)
	// 先在沒有 embedder 的狀態下存兩筆，模擬「Ollama 曾經離線」。
	d.Store(&Memory{Type: "til", Content: "first"})
	d.Store(&Memory{Type: "til", Content: "second"})

	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1, 0.2}, model: "fake-v1"})

	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Processed != 2 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want {Total:2 Processed:2 Failed:0}", stats)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestReindexIsIdempotent(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake-v1"})
	d.Store(&Memory{Type: "til", Content: "first"})

	if _, err := d.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 {
		t.Fatalf("second reindex Total = %d, want 0 (already up to date)", stats.Total)
	}
}

func TestReindexRefreshesOnModelChange(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake-v1"})
	d.Store(&Memory{Type: "til", Content: "first"})
	if _, err := d.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 換模型後，同一筆記憶應該被視為需要重算。
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.2}, model: "fake-v2"})
	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Processed != 1 {
		t.Fatalf("stats = %+v, want {Total:1 Processed:1}", stats)
	}
}

func TestReindexWithoutEmbedderNoOp(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "first"})

	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReindexStats{}) {
		t.Fatalf("stats = %+v, want zero value", stats)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db/... -run 'Reindex' -v`
Expected: FAIL — `Reindex`/`ReindexStats` 未定義

- [ ] **Step 3: Write the implementation**

Create `internal/db/reindex.go`:

```go
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
		if err != nil {
			stats.Failed++
			continue
		}
		if err := d.upsertEmbedding(p.id, vec, d.embedder.Model()); err != nil {
			stats.Failed++
			continue
		}
		stats.Processed++
	}
	return stats, nil
}
```

`upsertEmbedding` 是 Task 3 在 `internal/db/db.go` 新增的方法（見 Task 3 Step 3 第 5 點），這裡直接重用，不重寫 SQL。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/db/... -v`
Expected: PASS（全部測試）

- [ ] **Step 5: Commit**

```bash
git add internal/db/reindex.go internal/db/reindex_test.go
git commit -m "feat: add Reindex to backfill or refresh semantic embeddings"
```

---

### Task 6: CLI 接線（`openDB` 自動接上 embedder、新增 `reindex` 指令）+ 文件

**Files:**
- Modify: `internal/cli/commands.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `embed.NewOllamaEmbedderFromEnv()`（Task 1）、`(*db.DB).SetEmbedder`（Task 3）、`(*db.DB).Reindex`（Task 5）

- [ ] **Step 1: Modify `openDB()` to wire the embedder**

Edit `internal/cli/commands.go`，把 import 區塊換成（新增 `"context"` 與 `"memory-mcp/internal/embed"`）：

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"memory-mcp/internal/db"
	"memory-mcp/internal/embed"
	"memory-mcp/internal/httpapi"
	memcp "memory-mcp/internal/mcp"

	"github.com/spf13/cobra"
	mcpserver "github.com/mark3labs/mcp-go/server"
)
```

把 `openDB()` 改成：

```go
func openDB() (*db.DB, error) {
	path := dbPath
	if path == "" {
		path = os.Getenv("MEMORY_MCP_DB")
	}
	if path == "" {
		path = defaultDBPath()
	}
	d, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	d.SetEmbedder(embed.NewOllamaEmbedderFromEnv())
	return d, nil
}
```

- [ ] **Step 2: Add the `reindex` command**

在 `contextCmd` 定義之後、`importCmd` 之前加入（`context.Background()` 使用的是上一步已加入的 `"context"` import）：

```go
var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Backfill or refresh semantic search embeddings for stored memories",
	RunE: func(cmd *cobra.Command, args []string) error {
		if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
			return fmt.Errorf("reindex only operates on the local database, not --remote")
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		stats, err := d.Reindex(context.Background())
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(stats)
		}
		fmt.Printf("Reindexed %d/%d memories (%d failed)\n", stats.Processed, stats.Total, stats.Failed)
		return nil
	},
}
```

在 `init()` 的 `rootCmd.AddCommand(...)` 加上 `reindexCmd`：

```go
	rootCmd.AddCommand(storeCmd, searchCmd, listCmd, deleteCmd, updateCmd, statsCmd, exportCmd, importCmd, serveCmd, contextCmd, reindexCmd)
```

- [ ] **Step 3: Build and manually verify**

Run: `go build -o bin/memory-mcp . && ./bin/memory-mcp reindex`
Expected: 指令正常結束（exit code 0）。本機沒裝 Ollama 時，輸出形如 `Reindexed 0/N memories (N failed)`（每筆待補記憶的 Embed 呼叫都失敗，Failed 等於待補筆數）；不 panic、不掛住。之後在裝好 Ollama 的中央機器上重跑同一指令，預期看到 `Reindexed N/N memories (0 failed)`。

- [ ] **Step 4: Update README**

在 README.md 的 `## Search` 段落之前插入新段落：

```markdown
## Semantic Search

`search` 自動混合 FTS5 關鍵字搜尋與本機 Ollama 產生的語意向量搜尋（Reciprocal Rank Fusion）。Ollama 不在線時自動降級為純 FTS5，不影響既有使用方式。

```bash
# 需要本機跑著 Ollama 並已拉下 embedding 模型（預設 nomic-embed-text）
ollama pull nomic-embed-text

# 補算既有資料、或 Ollama 曾離線期間漏算的 embedding；可重複執行
memory-mcp reindex
```

環境變數（皆有預設值）：
- `MEMORY_MCP_OLLAMA_URL`（預設 `http://localhost:11434`）
- `MEMORY_MCP_EMBED_MODEL`（預設 `nomic-embed-text`）

跨機器同步（`--remote`）不需要裝 Ollama：語意運算全部發生在中央機器（跑實體 DB 那台）。
```

- [ ] **Step 5: Run full test suite and lint**

Run: `make test && make lint`
Expected: 全部套件測試通過，`go vet` 無警告

- [ ] **Step 6: Commit**

```bash
git add internal/cli/commands.go README.md
git commit -m "feat: wire semantic embedder into CLI, add reindex command"
```
