# memory-mcp

Cross-session persistent memory for AI coding agents. CLI-first, SQLite FTS5, zero dependencies.

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
  memory-mcp store -t feedback --tags tag1,tag2 "preference"

Recall when starting a task or need context:
  memory-mcp search "keyword"
  memory-mcp list --since 7d
```

### Codex / Gemini / Other LLM Agents

Add to `AGENTS.md`, `GEMINI.md`, or equivalent instructions file:

```markdown
# Cross-session memory

Store valuable technical knowledge, preferences, and solutions:
  memory-mcp store -t <type> [--tags t1,t2] [--project name] "content"

Types: feedback (preferences), til (solutions), summary (session summaries), knowledge (cross-project knowledge)

Recall when needed:
  memory-mcp search "keyword"
  memory-mcp list [--type type] [--since 7d]
```

## DB Location

Default: `~/.local/share/memory-mcp/memory.db`

Override: `--db` flag or `MEMORY_MCP_DB` env var.

## Search

Uses SQLite FTS5 with trigram tokenizer. Supports any language including CJK. Queries need at least 3 characters.
