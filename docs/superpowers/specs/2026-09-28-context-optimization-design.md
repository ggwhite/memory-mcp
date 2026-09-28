# Context 注入與檢索優化 Design

**Goal:** 降低記憶注入的 token 成本，並把檢索改成「先索引、再取全文」。另外補上敏感內容過濾與自動 session summary。

**背景 / 動機：** 參考 claude-mem 的做法（漸進揭露檢索、`<private>` 標記、自動 summary），但維持本專案單一 Go binary、資料不外送第三方壓縮服務的定位。

## 現況問題

- `~/dotfiles/claude/settings.json` 的 UserPromptSubmit hook 每則 prompt 都跑 `memory-mcp context --limit 20`。
- 平均每筆記憶約 1,280 字，20 筆全文約 25k 字。每則 prompt 都多出這些內容，而且在對話中一輪一輪累積。
- hook 不帶 project。`infinite`（293 筆）與 `yuna`（291 筆）占一半資料，任何專案都會被注入不相關的記憶。
- `memory_search` 一次回全文，agent 無法先看索引再挑選。
- 收尾 summary 依賴 agent 記得存，常漏。

## 範圍

四項功能：

1. SessionStart context 注入（取代 UserPromptSubmit）
2. 漸進揭露檢索：`memory_search` compact 模式、`memory_get`、`memory_timeline`
3. `<private>` 與金鑰格式過濾
4. SessionEnd 自動 summary（`claude -p`）

不在範圍內：

- Codex 的 hook 設定（Codex 繼續用 MCP tool）
- 依每則 prompt 做 relevance search 注入
- Web UI
- 既有記憶的回溯遮蔽

## 1. SessionStart context 注入

### CLI

`memory-mcp context --hook`：

- 從 stdin 讀 Claude Code hook JSON，取 `cwd`。
- project = `strings.ToLower(filepath.Base(cwd))`。與 core.md 的 project 命名規則一致。
- stdin 沒有 JSON 或解析失敗時，改用 `os.Getwd()`。
- 輸出到 stdout，Claude Code 把 SessionStart hook 的 stdout 當 context 注入。
- 任何錯誤都 exit 0 且不輸出，NEVER 讓 hook 失敗擋住 session。

未帶 `--hook` 時，行為維持既有 flag（`--type`、`--project`、`--limit`），但輸出改為精簡格式，另加 `--full` 回到全文格式。

### 輸出內容

依序三段，每段沒有資料就省略：

1. **全域 feedback**：`type=feedback` 且 `project IN ('', 'global')`，最近 10 筆。
2. **本專案 summary**：`type=summary` 且 `project=<p>`，最近 3 筆。
3. **本專案其他記憶**：`type IN (til, knowledge, feedback)` 且 `project=<p>`，最近 10 筆。

每筆一行：

```
- #1030 summary 2026-09-27 log 清理精簡版 24 MR 全部合併進 release 分支、24 張 Work Item 已關閉…
```

- 內容取前 120 個 rune，換行換成空白，超過加 `…`。
- 總輸出上限 6,000 rune。超過時從第 3 段尾端開始刪。
- 結尾固定兩行：
  - `全文用 memory_get(ids)，前後脈絡用 memory_timeline(id)。`
  - `(約 N tokens)`：N = 輸出 rune 數估算（CJK rune 算 1，其他 rune 每 4 個算 1）。

### DB 層

`ContextOptions` 新增欄位：

```go
type ContextOptions struct {
    Type    string
    Project string
    Limit   int
    Full    bool // true = 舊的全文分組格式
}
```

- `Project` 有值且 `Type` 為空時，走上面三段式輸出，`Limit` 不套用。
- 其他情況：依 `Type`／`Project` 過濾、最近 `Limit` 筆，精簡格式（`Full=false`）或全文格式（`Full=true`）。

### MCP tool

`memory_context` 新增 `full` bool 參數（預設 false），description 改為說明回傳索引、全文要再用 `memory_get`。

### Hook 設定

`~/dotfiles/claude/settings.json`：

- 刪除 UserPromptSubmit 的 memory-mcp hook。
- SessionStart 新增一組 hook，matcher `^(startup|resume|clear|compact)$`，command `~/github/memory-mcp/bin/memory-mcp context --hook`，timeout 10。

## 2. 漸進揭露檢索

### `memory_search`

- 新增 `compact` bool 參數，預設 true。
- compact 回傳每筆一行：`#id type project 日期 前80字…`。
- `compact=false` 維持現有全文輸出。
- CLI `memory-mcp search` 新增 `--compact` flag，預設 false（人類在終端機看全文較方便）。

### `memory_get`

- MCP tool `memory_get(ids)`：`ids` 是逗號分隔的 id 字串（例如 `"1030,1027"`），最多 20 個。
- 回傳每筆全文，格式與現有 `formatMemory` 一致。
- 找不到的 id 在結果中標示 `#id not found`，不整體報錯。
- CLI：`memory-mcp get <id>...`。

### `memory_timeline`

- MCP tool `memory_timeline(id, before=3, after=3)`：取與該筆記憶同一個 project、依 `created` 排序的前後各 N 筆，連同該筆本身，精簡格式輸出，中心那筆標 `→`。
- project 為空時以「project 為空」為同一組。
- before／after 上限 10。
- CLI：`memory-mcp timeline <id> [--before N] [--after N]`。

### DB 層與 Store 介面

`Store` interface 新增：

```go
GetMany(ids []int64) ([]Memory, error)
Timeline(id int64, before, after int) ([]Memory, error)
```

- `*DB` 實作。
- `httpapi.Client` 與 server 新增 `GET /v1/memories?ids=1,2,3` 與 `GET /v1/memories/{id}/timeline?before=&after=`。

`SearchResult` 已有 `Memory`，compact 格式在 MCP／CLI 的呈現層處理，DB 層不變。

## 3. `<private>` 與金鑰過濾

### 位置

新檔 `internal/db/sanitize.go`，函式：

```go
func Sanitize(content string) (clean string, redacted int)
```

`DB.Store` 與 `DB.Update` 寫入前呼叫。CLI、MCP、HTTP（remote 由 server 端 `*DB` 處理）三條路徑都會經過。

`ImportBatch` 不呼叫 Sanitize，匯入資料保持原樣以維持同步冪等。

### 規則

依序：

1. 刪除 `<private>…</private>`（跨行、非貪婪、不分大小寫），每段算 1 處。
2. 以下格式換成 `[REDACTED]`，每個 match 算 1 處：

| 名稱 | 規則 |
|---|---|
| PEM private key | `-----BEGIN [A-Z ]*PRIVATE KEY-----` 到對應 END 整段 |
| AWS access key | `AKIA[0-9A-Z]{16}` |
| Anthropic key | `sk-ant-[A-Za-z0-9_-]{20,}` |
| OpenAI 類 key | `sk-[A-Za-z0-9]{20,}` |
| GitHub token | `gh[pousr]_[A-Za-z0-9]{36,}` |
| GitLab token | `glpat-[A-Za-z0-9_-]{20,}` |
| JWT | `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}` |
| key=value | `(?i)(password\|passwd\|pwd\|secret\|token\|api[_-]?key)\s*[:=]\s*["']?([^\s"',;]{6,})`，只換掉值（第 2 組） |

「token 輪換」「providerpassword」這類沒有 `:`／`=` 接值的文字不會命中。

### 回傳

- `Memory` 新增 `Redacted int \`json:"redacted,omitempty"\``，Store／Update 後寫回。
- HTTP `/v1/store` 回應帶 `redacted`，`Client.Store` 寫回 `mem.Redacted`。
- MCP `memory_store` 在 `Redacted > 0` 時於回應附加 `（已遮蔽 N 處敏感內容）`。CLI 同樣輸出到 stderr。
- Sanitize 回傳前對結果做 `strings.TrimSpace`。
- Sanitize 後內容為空字串時回錯誤 `content empty after sanitize`，不寫入。

## 4. SessionEnd 自動 summary

### 流程

```
Claude Code SessionEnd
  └─ memory-mcp summarize --hook          （讀 stdin，檢查跳過條件，fork 背景子程序後 exit 0）
       └─ memory-mcp summarize --worker <tmpfile>   （背景，setsid）
            ├─ 解析 transcript
            ├─ claude -p --model haiku --no-session-persistence
            └─ Store(type=summary, tags=auto-summary, project=<p>)
```

### 前景（`--hook`）

1. `MEMORY_MCP_SUMMARIZING=1` 存在 → exit 0（防止 `claude -p` 自己的 SessionEnd 再觸發）。
2. 讀 stdin hook JSON：`session_id`、`transcript_path`、`cwd`、`reason`。
3. 把 hook JSON 寫到 `os.TempDir()` 下的暫存檔。
4. 以 `exec.Command(os.Args[0], "summarize", "--worker", tmpfile)` 啟動子程序，`SysProcAttr{Setsid: true}`，stdin／stdout／stderr 接 `/dev/null`，`Start()` 後不 `Wait()`，立即 exit 0。

`context --hook` 也檢查 `MEMORY_MCP_SUMMARIZING=1`，存在就不輸出，避免 `claude -p` 被注入記憶。

### 背景（`--worker`）

1. 讀暫存檔後刪除。
2. 解析 transcript JSONL：
   - 只取 `type=user` 且 content 為文字（排除 tool_result）與 `type=assistant` 的 text block。
   - 記錄第一筆訊息的 timestamp 為 session 開始時間。
3. 跳過條件（寫一行 log 後結束）：
   - user 文字訊息少於 3 則。
   - 同 project 在 session 開始時間之後已有 `type=summary` 的記憶（手動 summary 已存）。
4. 組對話文字：`[user] …` / `[assistant] …`，超過 60,000 rune 時保留最後 60,000 rune。
5. 呼叫 `claude -p --model haiku --no-session-persistence`，prompt 經 stdin 傳入，env 加 `MEMORY_MCP_SUMMARIZING=1`，timeout 3 分鐘。
6. 輸出 trim 後為 `SKIP` 或空字串 → 不存。
7. 否則 `Store(&Memory{Type: "summary", Content: out, Tags: "auto-summary", Project: p})`，一樣經過 Sanitize。

### Prompt

```
以下是一段 Claude Code 對話紀錄（project: <p>，日期: <YYYY-MM-DD>）。
用繁體中文寫一段工作摘要，600 字以內，開頭寫日期，包含：
1. 做了什麼
2. 關鍵決定與理由
3. 還沒做完的部分
保留具體識別資訊（檔案路徑、MR 編號、指令、主機名）。
不要寫出密碼、金鑰、token 的值。
如果這段對話只是閒聊、沒有實際工作內容，只輸出 SKIP。

<transcript>
…
</transcript>
```

### Log

所有跳過原因與錯誤 append 到 `~/.local/share/memory-mcp/summarize.log`，格式 `時間 session_id project 訊息`。

### Hook 設定

`~/dotfiles/claude/settings.json` 新增 SessionEnd hook：`~/github/memory-mcp/bin/memory-mcp summarize --hook`，timeout 10。

## 5. 文件同步

`~/dotfiles/agents/core.md` 的 memory 段落：

- tool 清單加 `memory_get`／`memory_timeline`，說明 search 預設只回索引，需要全文時用 `memory_get`。
- 收尾 summary 規則補一句：Claude Code 有 SessionEnd 自動 summary；手動存了就不會重複產生，重要決定仍一律手動存。

README 補上新 CLI 指令與 hook 設定範例。

## 錯誤處理

- 所有 hook 模式（`context --hook`、`summarize --hook`）一律 exit 0，錯誤只寫 log，不影響 Claude Code。
- `claude` 不在 PATH、timeout、非零退出：寫 log，不存。
- DB 開啟失敗：寫 log，不存。

## 測試

單元測試：

- `sanitize_test.go`：每條規則命中與不命中各一例，含 `<private>` 跨行、多段、中文文字不誤判。
- `db_test.go`：`GetMany`（含不存在 id）、`Timeline`（邊界：最前、最後、project 為空）、`Context` 三段式輸出與 6,000 rune 截斷、`Full` 格式、Store 後 `Redacted` 回寫、sanitize 後空字串報錯。
- `internal/httpapi/server_test.go`：新端點與 `Client.Store` 的 `Redacted` 回傳。
- `internal/cli` summarize：以 fixture transcript 測解析（排除 tool_result）、少於 3 則跳過、已有手動 summary 跳過、60,000 rune 截斷。`claude -p` 以可替換的 command 變數注入假程式測試。

手動端到端：

1. 在 `~/github/memory-mcp` 開新 session，確認注入內容只有全域 feedback 與 `memory-mcp` 專案記憶，且顯示 token 估計。
2. 對話 3 則以上後退出，1 分鐘內 `memory-mcp list --type summary --limit 1` 出現 `auto-summary`，`summarize.log` 無錯誤。
3. 同一 session 手動存 summary 後退出，確認沒有產生 auto-summary。
4. `memory-mcp store -t til "password=abcdef123 <private>x</private>"`，確認存入內容為 `password=[REDACTED]` 且 stderr 顯示遮蔽 2 處。
