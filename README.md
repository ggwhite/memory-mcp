# memory-mcp

Cross-session persistent memory for AI coding agents. CLI-first, SQLite FTS5, zero dependencies.

![demo](demo.gif)

## Install

```bash
git clone https://github.com/user/memory-mcp.git
cd memory-mcp
make build
# binary: bin/memory-mcp
```

Add to PATH or use absolute path.

## CLI

```bash
# Store
memory-mcp store -t til "SQLite trigram tokenizer 支援 CJK 搜索"
memory-mcp store -t feedback --tags testing,go --project myapp "用 real DB 跑 integration test"

# Search (3+ chars for CJK)
memory-mcp search "trigram"
memory-mcp search "全文搜索"

# List / manage
memory-mcp list [--type type] [--since 7d]
memory-mcp search --compact "trigram"   # 一行一筆索引
memory-mcp get 12 34                    # 取全文
memory-mcp timeline 12 --before 3 --after 3
memory-mcp update <id> "new content"
memory-mcp delete <id>
memory-mcp stats

# Export / Import
memory-mcp export > backup.json
memory-mcp import backup.json
```

Memory types: `feedback`, `til`, `summary`, `knowledge`

Global flags: `--json` (JSON output), `--db <path>` (override DB path)

## AI Agent Integration

### Claude Code (MCP Server)

```bash
claude mcp add memory -s user -- /path/to/memory-mcp serve
```

Then add to your `CLAUDE.md` or global instructions:

```markdown
# Cross-session memory

Store valuable technical knowledge, preferences, and solutions for recall across sessions:
  memory-mcp store -t til "description"
  memory-mcp store -t feedback --project global --tags tag1,tag2 "preference"

Recall when starting a task or need context:
  memory-mcp search "keyword"
  memory-mcp list --since 7d
```

### Claude Code hooks

```json
"SessionStart": [{"matcher": "^(startup|resume|clear|compact)$", "hooks": [
  {"type": "command", "command": "/path/to/memory-mcp context --hook", "timeout": 10}
]}],
"UserPromptSubmit": [{"hooks": [
  {"type": "command", "command": "/path/to/memory-mcp recall --hook", "timeout": 5}
]}],
"SessionEnd": [{"hooks": [
  {"type": "command", "command": "/path/to/memory-mcp summarize --hook", "timeout": 10}
]}]
```

- `context --hook`：以工作目錄推算 project（規則見下方「Project 推導」），注入全域 feedback（project=global）、專案 feedback（最多 20 筆）、專案最近 3 筆 summary、其他 til/knowledge 10 筆的索引（上限 6,000 字，超過時從 til/knowledge 開始裁，feedback 最後才裁；`personal-` 開頭的 project 與 `home` 只注入全域 feedback）。注入過的 id 會記進 recall 的 session 狀態檔。
- `recall --hook`：每則訊息依內容注入最多 3 筆相關記憶的索引行（規則見下方「recall」）。
- `summarize --hook`：背景呼叫 `claude -p --model haiku` 摘要本次 session，存成 `summary`（tag `auto-summary`）。session 中已手動存 summary、或 user 訊息少於 3 則時跳過。log 在 DB 同目錄的 `summarize.log`。
- 存入內容會刪除 `<private>…</private>` 並遮蔽常見金鑰格式。

#### Project 推導

`context --hook`、`recall --hook` 與 `summarize --hook` 依 hook 傳入的 `cwd` 推算 project，順序如下：

1. 讀路徑前綴對照表 `~/.config/memory-mcp/projects.json`（可用 `MEMORY_MCP_PROJECTS` env 指定其他路徑），以**路徑元件為邊界**做最長前綴比對，命中就用對照值（轉小寫）。用來讓多 repo 工作區的子目錄（例如 `~/Tyche/Infinite/infinite/old-bi`）歸到同一個 project。
2. 沒命中且 `cwd` 就是 `$HOME` 時，回 `home`。
3. 其餘用工作目錄 basename（小寫）。

```json
{"prefixes": {"~/Tyche/Infinite": "infinite", "~/github/memory-mcp": "memory-mcp"}}
```

- 前綴與 `cwd` 都會展開 `~`、正規化並解 symlink；`~/Tyche/Infinite2` 不會命中 `~/Tyche/Infinite`。
- 設定檔不存在時直接退回 basename；格式錯誤時往 stderr 印一行警告後同樣退回 basename。

#### recall（UserPromptSubmit）

讀 hook JSON 的 `prompt`，用 FTS5 trigram 撈出相關記憶，stdout 輸出（Claude Code 會把它當 context 注入）：

```
## 相關記憶（依本則訊息自動檢索，需要全文用 memory_get）
- #1347 knowledge infinite 2026-10-09 218-tx 分层玩家導出卡「上传中」＝遊客號暴增（渠道 64001）撞 getUserLevelLog 10 萬筆上限…
- 筆記 ~/ob/追劇/耘荒記.md
```

- 去頭尾空白後少於 4 個字元、或以 `/` 開頭的訊息不檢索。超過 500 字時只取開頭 250 字＋結尾 250 字（中間以空白分隔，避免頭尾黏成假的 3 字窗口）切詞：貼長 log 時中段只會拖慢逐筆比對，而提問可能在 log 之前或之後。截斷時 24 個查詢詞名額頭尾各 12（頭段取最前面的詞、尾段取最後面的詞，一邊用不完的名額給另一邊），避免多樣性高的 log 把另一端的提問擠掉。
- 記憶行格式：`- #<id> <type> <project> <建立日期> <內容前 60 字>`（日期是 DB 的 created，UTC）；筆記行格式：`- 筆記 ~/ob/<相對路徑>`。
- 切詞：英數連續段（含內部 `-_.`，例如 `218-tx`、`memory-mcp`）≥3 字元直接當查詢詞；CJK 連續段取所有重疊 3 字窗口；含停用詞（怎麼、什麼、是不、可以、上次…）的窗口丟掉。查詢詞以 OR 組成 FTS5 查詢，依 bm25 排序。
- 2 字 CJK 詞（注入、點數…）trigram 無法 MATCH，不拿來撈候選，只在候選內做子字串比對：每命中一個加 2 分，並用來計算「命中片段數」。
- 門檻（寧可漏不可亂給）：分數 ≥ 10、至少命中訊息中 2 個互不相連的片段、且不低於第一名的 60%，最多 3 筆。門檻用約 1,250 筆的真實 DB 校準；DB 筆數少很多時 bm25 的 IDF 較小，會更不容易過門檻。
- project 範圍：`cwd` 命中前綴對照表（登記過的工作專案）時排除 `personal-*`；`home` 或未登記的目錄不限。同 project 的記憶排序分數 ×1.15（只影響排序，不影響門檻）。
- 去重：同一 session 已注入過的 id（含 `context --hook` 注入的）不再注入，狀態存在 `$TMPDIR/memory-mcp/recall-<session_id>.json`。
- Obsidian 筆記：只在 `cwd` **沒有**命中前綴對照表（`home` 或未登記目錄）時啟用。vault 預設 `~/ob`（會解 symlink），可用 `MEMORY_MCP_OB` 覆寫，不存在就略過。只比對 `.md` 的檔名（不含副檔名）與上層目錄名，不讀內容；略過 `.` 開頭目錄、`assets`、`raw`，以及 index／log／README／AGENTS／CLAUDE／SUMMARY／todo 等泛用檔名。門檻：名稱中至少 3 個字被 3 字以上的查詢詞覆蓋且覆蓋率 ≥30%，或名稱 ≥4 字且（含 2 字詞）覆蓋率 ≥80%；檔名命中排在目錄名命中之前，最多 2 筆，接在記憶行之後輸出為 `- 筆記 ~/ob/<相對路徑>`，同 session 以路徑去重。
- 筆記清單快取在 `$TMPDIR/memory-mcp/ob-index.json`，vault 根目錄 mtime 改變或超過 60 秒就重建，快取壞掉也重建；走訪比整體期限早 50ms 放棄，逾時只略過筆記、記憶照常輸出。記憶（DB）與筆記（vault）平行查詢，DB 逾時或鎖住時仍會輸出期限內完成的筆記。
- 比例門檻（第一名的 60%）以去重前的最高分為基準：第一名已注入過時，原本不夠格的次名不會因此浮上來。
- 只查本機 DB，不呼叫 embedding 服務；設定 `--remote`／`MEMORY_MCP_REMOTE` 時不支援。整個指令 300ms 內完成，任何錯誤（壞 JSON、DB 鎖、逾時、panic）只往 stderr 寫一行、stdout 空、exit 0，不會擋住訊息送出（UserPromptSubmit hook exit 2 會擋訊息，所以進入點與背景查詢都有 recover）。

### Codex / Gemini / Other LLM Agents

Add to `AGENTS.md`, `GEMINI.md`, or equivalent instructions file:

```markdown
# Cross-session memory

Store valuable technical knowledge, preferences, and solutions:
  memory-mcp store -t <type> [--tags t1,t2] [--project name] "content"

Types: feedback (preferences; project required, use `global` for cross-project rules), til (solutions), summary (session summaries), knowledge (project knowledge)

Session start injects only: feedback with project=global, plus the current project's feedback, summaries and til/knowledge. Memories with an empty project or a `personal-*` project are never injected.

Recall when needed:
  memory-mcp search "keyword"
  memory-mcp list [--type type] [--since 7d]
```

## DB Location

Default: `~/.local/share/memory-mcp/memory.db`

Override: `--db` flag or `MEMORY_MCP_DB` env var.

## Semantic Search

`search` 自動混合 FTS5 關鍵字搜尋與本機 Ollama 產生的語意向量搜尋（Reciprocal Rank Fusion）。Ollama 不在線時自動降級為純 FTS5，不影響既有使用方式。

```bash
# 需要本機跑著 Ollama 並已拉下 embedding 模型（預設 bge-m3）
ollama pull bge-m3

# 補算既有資料、或 Ollama 曾離線期間漏算的 embedding；可重複執行
memory-mcp reindex
```

環境變數（皆有預設值）：
- `MEMORY_MCP_OLLAMA_URL`（預設 `http://localhost:11434`）
- `MEMORY_MCP_EMBED_MODEL`（預設 `bge-m3`）

跨機器同步（`--remote`）不需要裝 Ollama：語意運算全部發生在中央機器（跑實體 DB 那台）。

## Search

Uses SQLite FTS5 with trigram tokenizer. Supports any language including CJK. Queries need at least 3 characters.
