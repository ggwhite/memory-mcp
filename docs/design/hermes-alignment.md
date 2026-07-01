# Hermes Agent 記憶架構對齊分析

> 日期：2026-07-01

## 背景

[Hermes Agent](https://github.com/NousResearch/hermes-agent) 是 NousResearch 開發的開源 AI agent，其記憶系統被認為是目前最成熟的 agent 記憶實作之一。本文件分析 Hermes 的設計，對比 memory-mcp 現狀，並提出可行的對齊方案。

## Hermes 記憶架構摘要

### 三層架構

| 層級 | 儲存 | 注入方式 | 容量 |
|------|------|----------|------|
| Tier 1 — 即時記憶 | `MEMORY.md` + `USER.md`（Markdown 檔案） | session 開始時凍結注入 system prompt | ~1,300 tokens（2,200 + 1,375 chars） |
| Tier 2 — Session 搜尋 | SQLite + FTS5（`state.db`） | 按需透過 `session_search` tool | 無上限 |
| Tier 3 — 外部 Provider | Honcho / Mem0 / OpenViking 等 8 種 | prefetch + 專用 tool | 依 provider |

### 關鍵設計

1. **Frozen Snapshot Pattern** — MEMORY.md / USER.md 在 session 開始時凍結注入 system prompt，mid-session 寫入持久化到磁碟但不更新 prompt（保留 LLM prefix cache 效能）
2. **Bounded + Curated** — 有限容量 + 80% 時強制整合，比無限累積更有效
3. **Lifecycle Hooks** — `MemoryManager` 透過 `on_turn_start`（prefetch）、`sync_turn`（同步）、`on_session_end`（萃取）驅動自動記憶
4. **Session Search** — FTS5 全文搜尋，三種模式：DISCOVERY（搜尋 + context window）、SCROLL（錨點窗口）、BROWSE（時間列表）
5. **System Prompt 分層** — Stable tier（身份）→ Context tier（skill/文件）→ Volatile tier（記憶/profile/時間戳，每 turn 重組）

### 限制

- 跨 agent 記憶共享尚未完成（MEMORY.md 無法透過 MCP 暴露）
- 外部 provider 一次只能啟用一個
- 記憶容量硬限制需要積極策展

## memory-mcp 現狀

### 架構

```
memory-mcp
├── CLI（store / search / list / delete / update / stats / export / import）
├── MCP Server（stdio，4 tools：memory_store / search / list / delete）
└── SQLite + FTS5（trigram tokenizer，支援 CJK）
```

### 對比

| 維度 | Hermes | memory-mcp | 差距 |
|------|--------|------------|------|
| 即時記憶注入 | MEMORY.md 凍結注入 system prompt | ❌ 無 — 依賴 CLAUDE.md 指示 agent 手動搜 | **大** |
| 記憶容量管理 | bounded（2,200 chars）+ 自動整合 | ❌ 無上限、無整合 | 中 |
| 全文搜尋 | FTS5 + LLM summarization | FTS5 trigram（已支援 CJK） | 小 |
| Session 歷史 | 完整對話存 SQLite，可搜尋 | ❌ 只存記憶條目，不存對話 | **大** |
| 自動觸發 | lifecycle hooks（turn start/end） | ❌ 完全依賴 system prompt 提醒 | **大** |
| 記憶類型 | agent notes + user profile（兩檔案） | feedback / til / summary / knowledge（4 type） | 持平 |
| 跨 LLM 通用 | ❌ 只支援自己的 runtime | ✅ CLI-first，任何 LLM 都能用 | memory-mcp 勝 |
| 外部 provider | 8 種 plugin | ❌ 無 | 中（暫不需要） |

## 對齊方案

### 原則

memory-mcp 不是 agent runtime，不能控制 LLM 的對話 loop。對齊策略應該是：**在 MCP / CLI 的約束下，用最少的改動最大化記憶的自動性和有效性。**

### Phase 1：Context Snapshot（對齊 Tier 1）

**目標**：讓 agent 每次 session 自動看到最重要的記憶，不用手動搜。

新增 `memory-mcp context` 命令，產生一份 bounded 的記憶摘要：

```
memory-mcp context [--limit 20] [--max-chars 2000] [--project name]
```

輸出格式：
```markdown
## Recent Memories (12/45 total)

### feedback (3)
- #42 不要 mock database，用 real DB 跑 integration test
- #38 PR 描述要寫 why，不只是 what
- #35 commit message 用英文

### til (5)
- #67 SQLite FTS5 trigram tokenizer 支援 CJK 搜尋
- #65 Go 1.26 iter.Pull 可以把 push iterator 轉 pull
...
```

**整合方式**：在 Claude Code hook（`UserPromptSubmit`）或 CLAUDE.md 中加：

```bash
# settings.json hook
"UserPromptSubmit": "memory-mcp context --limit 15 --max-chars 1500"
```

這樣每次使用者送出 prompt 時，最近的記憶自動注入 context——等同 Hermes 的 frozen snapshot，但透過 hook 而非 runtime 整合。

### Phase 2：Smart Summary（對齊 bounded + curated）

**目標**：避免記憶無限膨脹，自動識別重複和過時條目。

新增 `memory-mcp gc` 命令：

```
memory-mcp gc [--dry-run] [--older-than 30d]
```

功能：
- 列出超過 N 天未被搜尋命中的記憶
- 標記可能重複的條目（content 相似度 > 閾值）
- `--dry-run` 預覽，確認後刪除

不做自動整合（需要 LLM），但提供資料讓 agent 決定。

### Phase 3：Session Log（對齊 Tier 2）

**目標**：保留對話摘要，讓跨 session 回憶更完整。

新增 `session` 類型的記憶，或獨立的 `sessions` 表：

```sql
CREATE TABLE sessions (
    id       TEXT PRIMARY KEY,  -- session ID
    summary  TEXT NOT NULL,
    project  TEXT NOT NULL DEFAULT '',
    created  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);
```

搭配 Claude Code 的 hook：

```bash
# session 結束時自動存摘要
"Stop": "memory-mcp session-end --id $SESSION_ID --summary '...'"
```

但 session 摘要需要 LLM 產生，hook 中跑 LLM 不實際。更務實的做法是在 CLAUDE.md 指示 agent 結束前主動存 summary：

```markdown
session 結束前（使用者說「下班」「結束」或切換任務時），用 memory-mcp store -t summary 存一句話摘要。
```

### Phase 4：MCP Tool 增強

新增 `memory_context` tool，讓 agent 可以在 MCP 層面取得 context snapshot：

```
memory_context(project?, limit?, max_chars?) → markdown summary
```

這讓 agent 在 turn 開始時可以主動呼叫，模擬 Hermes 的 `on_turn_start` prefetch。

## 優先級

| Phase | 改動量 | 價值 | 建議 |
|-------|--------|------|------|
| Phase 1 — context snapshot | 小（1 個 CLI 命令 + 1 個 hook） | **高** — 解決最大痛點 | ✅ 先做 |
| Phase 4 — MCP context tool | 小（1 個 tool） | 高 — Phase 1 的 MCP 版 | ✅ 一起做 |
| Phase 3 — session log | 中（新表 + CLAUDE.md 指示） | 中 | 其次 |
| Phase 2 — gc | 中（相似度計算） | 低（目前量不大） | 晚做 |

## 不做的事

- 不做 Hermes 的 lifecycle hooks runtime 整合 — 我們不是 agent runtime
- 不做外部 provider plugin 系統 — 複雜度太高，價值不明確
- 不做 LLM summarization — 保持零 API token 的定位
- 不做 bounded memory — memory-mcp 的定位是知識庫，不是 working memory；curation 交給 agent 或 gc
