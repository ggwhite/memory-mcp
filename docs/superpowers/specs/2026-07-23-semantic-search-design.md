# 語意向量搜尋 Design

**Goal:** 在現有 SQLite FTS5 關鍵字搜尋之外，加入語意向量搜尋，讓 `memory_search` 能找到關鍵字對不上但語意相關的記憶。混合排序（FTS5 + 向量），不破壞現有純關鍵字搜尋行為，也不破壞跨機器同步（`--remote`）。

**背景 / 動機：** 參考 claude-mem 專案的向量搜尋能力，但 claude-mem 用 Chroma（Python 向量資料庫）+ Bun worker，跟本專案「zero dependency 純 Go binary」的定位衝突，因此重新設計一套輕量版本。

## 限制與前提

- `internal/db` 用 `modernc.org/sqlite`（純 Go 移植，非 CGO 綁 libsqlite3.so），無法動態載入 `sqlite-vec` 這類 C extension。
- 目前記憶總量約 220 筆，成長速度慢（每天數筆），Go 端 brute-force cosine similarity 在此規模下是毫秒級運算，不需要額外的向量索引函式庫（如 HNSW）。
- Embedding 來源：本機 Ollama（`nomic-embed-text` 或同等 embedding 模型），只需安裝在「中央機器」——也就是實際跑 `*db.DB` 的那台（目前是公司 MacMini）；用 `--remote` 連過去的機器（家裡電腦）純轉發 HTTP request，不需要裝 Ollama。
- Ollama 不在線時，功能必須優雅降級：`Store` 照存（向量欄位留空）、`Search` 自動退回純 FTS5 排序，不拋錯、不中斷使用。

## 架構

### 新增資料表

```sql
CREATE TABLE IF NOT EXISTS memory_embeddings (
    memory_id INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector    BLOB NOT NULL,   -- little-endian float32 陣列
    model     TEXT NOT NULL,   -- 產生此向量的 embedding 模型名稱
    created   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);
```

`model` 欄位用來偵測「模型換過」的情況：`reindex` 時若記錄的 model 名稱跟目前設定的不同，視為需要重算。

### 新增 package `internal/embed`

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    Model() string
}

type OllamaEmbedder struct { /* baseURL, model, http.Client */ }
```

- `OllamaEmbedder.Embed` 直接呼叫 `POST {baseURL}/api/embed`（Ollama 原生 HTTP API），不引入額外 Go module。
- 設定來源（環境變數，皆有預設值，不加必填 CLI flag）：
  - `MEMORY_MCP_OLLAMA_URL`（預設 `http://localhost:11434`）
  - `MEMORY_MCP_EMBED_MODEL`（預設 `nomic-embed-text`）
- 呼叫逾時：沿用現有 httpapi.Client 的 10 秒慣例，設定合理短逾時（例如 3 秒），避免 Ollama 沒回應時拖慢 store/search。

### `db.DB` 整合點

- `Store()`：寫入 `memories` 成功後，嘗試 `embedder.Embed(content)`；成功則寫入 `memory_embeddings`；失敗（含逾時、連線拒絕）僅記錄不中斷，回傳原本的 store 結果。
- `Update()`：內容變更後，比照 Store 邏輯重新計算並覆蓋 embedding（同樣容錯）。
- `Delete()`：`ON DELETE CASCADE` 已處理 `memory_embeddings` 對應列刪除，無需額外程式碼。
- `Search()`：
  1. 照舊跑 FTS5 查詢，取得關鍵字排序結果。
  2. 嘗試對 `opts.Query` 呼叫 `embedder.Embed`；失敗則直接回傳純 FTS5 結果（現狀行為不變）。
  3. 成功則載入所有（或依 `type`/`project` 篩選後的）`memory_embeddings`，Go 端對全部候選計算 cosine similarity 並排序——目前規模（~220 筆）brute-force 全量計算即可，不需要提前截斷 top-N。
  4. 用 Reciprocal Rank Fusion（`score = Σ 1/(k + rank)`，`k=60` 業界慣用常數）合併 FTS5 排名與向量排名，取融合後前 `opts.Limit` 筆回傳。

`Store` interface（`db.Store`）簽章不變，`httpapi.Server`/`httpapi.Client` REST 介面完全不用改——向量邏輯全部封裝在中央機器的 `DB` 實作內部，跨機器同步不受影響。

### 新增 CLI 指令：`memory-mcp reindex`

- 掃描所有記憶，找出「沒有 embedding」或「embedding 的 model 欄位跟目前設定不同」的記錄，逐筆呼叫 Ollama 補算。
- 冪等：可重複執行，只處理缺漏/過期的部分。
- 用途：(1) 第一次啟用此功能時補算既有 ~220 筆記憶；(2) Ollama 曾離線導致某些 Store 漏算時事後補齊；(3) 換 embedding 模型後重新計算。
- 輸出進度（處理了幾筆、跳過幾筆、失敗幾筆），失敗不中斷整體流程。

## 測試

- `internal/embed`：用 `httptest.NewServer` 假 Ollama endpoint，測正常回應、逾時、非 200 狀態碼三種情境，不需要真的裝 Ollama 才能跑測試。
- `internal/db`：測 Store/Search 在「embedder 回傳成功」與「embedder 回傳錯誤」兩種情境下的行為，確認降級路徑（純 FTS5）正確運作、不拋錯。
- RRF 合併邏輯獨立寫成可測試的純函式（輸入兩組排名列表，輸出合併排序），單元測試涵蓋：其中一組為空、兩組完全不重疊、有重疊排名靠前。

## 不做的事（YAGNI）

- 不加 sqlite-vec 或任何 C extension（跟 modernc.org/sqlite 不相容，且目前規模不需要）。
- 不支援多種 embedding provider 切換（Ollama 之外的 OpenAI/Voyage 等），需要時再加。
- 不做 embedding 的背景非同步佇列，Store 時同步呼叫（Ollama 本機呼叫延遲通常在數十毫秒等級，可接受）。
- 不改動 `httpapi` REST 介面。
