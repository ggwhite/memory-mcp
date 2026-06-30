# memory-mcp — 跨 Session 持久記憶工具

> 日期：2026-06-30

## 定位

CLI-first 的持久記憶工具，讓 AI coding agent（Claude Code、Codex、Gemini）和人類共享跨 session、跨專案的知識。底層用 SQLite FTS5 全文搜索，零外部依賴、零 API token、完全離線。

獨立專案，放 `~/github/memory-mcp/`。

## 問題

- Claude Code 的 auto memory 是 per-project，跨專案記憶不共享
- Session 結束後對話歷史消失，沒有可搜索的紀錄
- 不同 LLM client（Claude Code / Codex / Gemini）之間記憶不互通
- MCP tool 每次呼叫消耗 token，CLI 直接操作零成本

## 設計決策

| 決策 | 選擇 | 理由 |
|------|------|------|
| 主要介面 | CLI-first，MCP 可選 | CLI 省 token、跨 LLM 通用 |
| 搜索方式 | SQLite FTS5 keyword | 技術記憶用 keyword 就夠準，不需要 embedding |
| 多人 | 各自 DB 檔 | 最簡單，不需要 auth |
| 與 auto memory 關係 | 並存 | 先觀察，之後再評估是否遷移 |
| 語言 | Go | 單一 binary，跟 4x 生態一致 |
| SQLite binding | modernc.org/sqlite | 純 Go，免裝 C compiler，跨平台 |

## 資料模型

```sql
CREATE TABLE memories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT NOT NULL CHECK(type IN ('feedback','til','summary','knowledge')),
    content  TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '',
    project  TEXT NOT NULL DEFAULT '',
    created  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE VIRTUAL TABLE memories_fts USING fts5(
    content, tags, project,
    content=memories, content_rowid=id
);

-- 觸發器：自動同步 FTS index
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;

CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
END;

CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;
```

### 記憶類型

| type | 用途 | 範例 |
|------|------|------|
| `feedback` | 跨專案偏好與工作方式 | 「不要加不必要 comment」「先建 feature 再寫 docs」 |
| `til` | 技術問題解決方案 | 「SIGPIPE + pipefail 的 race 用 herestring 解」 |
| `summary` | 對話/工作摘要 | 「今天修了 4x deep review cost 顯示 bug」 |
| `knowledge` | 專案間關聯知識 | 「4x protocol 設計影響了 kairos state machine」 |

## CLI 介面

```
memory-mcp store   --type <type> [--tags t1,t2] [--project name] <content>
memory-mcp search  <query> [--type type] [--limit N]
memory-mcp list    [--type type] [--limit N] [--since 7d]   # since: Nd 格式（天）
memory-mcp delete  <id>
memory-mcp update  <id> <content>
memory-mcp stats
memory-mcp export  [--format json]
memory-mcp import  <file>
memory-mcp serve                          # stdio MCP server
```

### 輸出格式

CLI 預設輸出人類可讀格式，加 `--json` 輸出 JSON（方便 LLM 解析）。

```
$ memory-mcp search "database testing"
#42 [feedback] 2026-06-28  tags:testing,go  project:kairos
  不要 mock database，用 real DB 跑 integration test

#67 [til] 2026-06-30  tags:sqlite,testing
  SQLite in-memory DB 用 :memory: 加 ?cache=shared 可以跨 connection 共享
```

```
$ memory-mcp search "database testing" --json
[{"id":42,"type":"feedback","content":"不要 mock database...","tags":"testing,go","project":"kairos","created":"2026-06-28T10:30:00","rank":-2.5}]
```

## MCP Server

`memory-mcp serve` 啟動 stdio MCP server，暴露 4 個 tool：

| Tool | 參數 | 回傳 |
|------|------|------|
| `memory_store` | `type` (required), `content` (required), `tags`, `project` | `{id, created}` |
| `memory_search` | `query` (required), `type`, `limit` (default 5) | `[{id, type, content, tags, project, created}]` |
| `memory_list` | `type`, `limit` (default 10), `since` | `[{id, type, content, tags, project, created}]` |
| `memory_delete` | `id` (required) | `{deleted: true}` |

### Claude Code 設定

```json
// ~/.claude/settings.json
{
  "mcpServers": {
    "memory": {
      "command": "/Users/white/github/memory-mcp/memory-mcp",
      "args": ["serve"]
    }
  }
}
```

### 其他 LLM 整合

Codex / Gemini / 任何能跑 shell 的 LLM，在 AGENTS.md 或等效指令檔加：

```markdown
# 跨 session 記憶

遇到值得記住的技術知識、偏好、解法時，用 CLI 存入全局記憶：
  memory-mcp store --type til "解法描述"

需要回憶時：
  memory-mcp search "關鍵字"
```

## DB 位置

`~/.local/share/memory-mcp/memory.db`

目錄不存在時自動建立。可用 `--db` flag 或 `MEMORY_MCP_DB` 環境變數覆蓋。

## 專案結構

```
~/github/memory-mcp/
  main.go              CLI entry point
  internal/
    db/
      db.go            SQLite 連線、schema migration、CRUD
      db_test.go
      search.go        FTS5 搜索邏輯
      search_test.go
    mcp/
      server.go        stdio MCP server
      server_test.go
    cli/
      commands.go      CLI 指令定義
  go.mod
  go.sum
  README.md
  .gitignore           memory.db, bin/
  Makefile
```

## 不做的事

- 不做語意搜索 / embedding（FTS5 keyword 就夠）
- 不做 HTTP server / daemon（CLI + stdio MCP 就夠）
- 不做多人共享 DB（各自的 DB 檔）
- 不做 auto memory 遷移（先並存觀察）
- 不做 UI / dashboard
