# memory-mcp

Cross-session persistent memory for AI coding agents — one Go binary, one SQLite file, usable from the CLI, as an MCP server, and from Claude Code hooks.

![demo](demo.gif)

## Why

AI coding agents forget everything when a session ends. The bug you fixed yesterday, the convention the user corrected twice, the half-finished refactor — all gone, and the next session starts from zero.

memory-mcp gives agents a small, durable, searchable memory that is **agent-agnostic**:

- Any agent that can run a shell command can use the CLI (`memory-mcp store`, `memory-mcp search`).
- Any MCP client (Claude Code, Codex, ...) can use the MCP tools.
- Claude Code hooks can inject relevant memories automatically, without the agent having to ask.

## Features

- **Full-text search that handles CJK** — SQLite FTS5 with the `trigram` tokenizer, so Chinese/Japanese/Korean text is searchable without a word segmenter (queries need 3+ characters).
- **Optional hybrid semantic search** — when a local Ollama is reachable, `search` fuses FTS5 and vector results with Reciprocal Rank Fusion; when it is not, it silently falls back to FTS5 only.
- **MCP server** — 7 tools over stdio or Streamable HTTP.
- **Full CLI** — store, search, list, get, timeline, update, delete, stats, export/import, reindex.
- **Claude Code hooks**
  - `SessionStart` → `context --hook` injects a bounded index of global rules plus the current project's memories.
  - `UserPromptSubmit` → `recall --hook` injects up to 3 memories relevant to the prompt, in under 300 ms, and never blocks the prompt.
  - `SessionEnd` → `summarize --hook` writes an automatic session summary in the background.
- **Token-efficient retrieval** — search and context return one index line per memory; full content is fetched on demand by id.
- **Remote mode** — run one central REST server (`serve --http-api`) and point other machines at it with `--remote` / `MEMORY_MCP_REMOTE`.
- **Secret hygiene** — `<private>...</private>` blocks are stripped and common key formats are replaced with `[REDACTED]` before anything is written.

## Architecture

memory-mcp is a single Go binary built on [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO), so it cross-compiles cleanly and has no runtime dependencies. All data lives in one SQLite database in WAL mode, by default at `~/.local/share/memory-mcp/memory.db`. The CLI, the MCP server, and the hooks are thin front-ends over the same `internal/db` package through a common `Store` interface; in remote mode that interface is backed by an HTTP client that talks to another memory-mcp instance running `serve --http-api`.

```
 CLI ─────────┐
 MCP (stdio / │
  HTTP) ──────┼──► db.Store ──► internal/db ──► SQLite (FTS5 trigram + embeddings)
 Hooks ───────┘        │                              ▲
                       └─ --remote ──► REST client ───┘ (serve --http-api on another host)
                                                       Ollama (optional, embeddings)
```

## Install

Requires Go 1.26.4 or newer (see `go.mod`).

```bash
go install github.com/ggwhite/memory-mcp@latest
```

Prebuilt binaries: see the [Releases](https://github.com/ggwhite/memory-mcp/releases) page (coming soon).

From source:

```bash
git clone https://github.com/ggwhite/memory-mcp.git
cd memory-mcp
make build          # produces bin/memory-mcp
```

## MCP setup

### Claude Code

```bash
claude mcp add memory -s user -- memory-mcp serve
```

Equivalent JSON (e.g. in `.mcp.json`):

```json
{
  "mcpServers": {
    "memory": { "command": "memory-mcp", "args": ["serve"] }
  }
}
```

### Codex

`~/.codex/config.toml`:

```toml
[mcp_servers.memory]
command = "memory-mcp"
args = ["serve"]
```

### Shared HTTP server

Instead of one stdio process per session, you can run one Streamable HTTP server and have every client connect to it:

```bash
memory-mcp serve --http 127.0.0.1:8766   # MCP endpoint: http://127.0.0.1:8766/mcp
```

### Tools

| Tool | Description |
|------|-------------|
| `memory_store` | Store a memory (`type`, `content`, optional `tags`, `project`). |
| `memory_search` | Hybrid search; returns index lines by default (`compact=false` for full JSON). |
| `memory_get` | Fetch full content by comma-separated ids (max 20). |
| `memory_timeline` | Memories stored before/after a given id in the same project (default 3, max 10 each side). |
| `memory_list` | Recent memories, optionally filtered by `type` and `since` (e.g. `7d`). |
| `memory_delete` | Delete a memory by id. |
| `memory_context` | Bounded index of global feedback plus a project's feedback, summaries, and til/knowledge. |

## Memory types

| Type | Use for |
|------|---------|
| `feedback` | User preferences and corrections. **Requires a project**; use `global` for rules that apply everywhere. |
| `til` | Technical solutions and non-obvious fixes. |
| `summary` | Session / work summaries. |
| `knowledge` | Project or cross-project architectural knowledge. |

## Claude Code hooks

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [{"matcher": "^(startup|resume|clear|compact)$", "hooks": [
      {"type": "command", "command": "memory-mcp context --hook", "timeout": 10}
    ]}],
    "UserPromptSubmit": [{"hooks": [
      {"type": "command", "command": "memory-mcp recall --hook", "timeout": 5}
    ]}],
    "SessionEnd": [{"hooks": [
      {"type": "command", "command": "memory-mcp summarize --hook", "timeout": 10}
    ]}]
  }
}
```

Each hook reads the Claude Code hook JSON from stdin (`session_id`, `cwd`, `prompt`, `transcript_path`) and never fails the hook: errors go to stderr or a log, and the exit code is 0.

- **`context --hook`** prints an index of: global feedback (`project=global`), the project's feedback, its 3 latest summaries, and other til/knowledge — capped at 6,000 characters (til/knowledge is trimmed first, feedback last). In a `personal-*` project or the `home` directory, only global feedback is injected. Memories with an empty project are never injected.
- **`recall --hook`** extracts query terms from the prompt (alphanumeric runs of 3+ chars and overlapping 3-character CJK windows), runs an FTS5 query, and prints at most 3 index lines that pass a conservative relevance threshold. Prompts shorter than 4 characters or starting with `/` are skipped, memories already injected in the same session are not repeated, and the whole command is bounded to 300 ms. It only queries the local DB (no embeddings) and does not support `--remote`.
- **`summarize --hook`** forks a background worker and returns immediately. The worker reads the transcript and shells out to `claude -p --model haiku` to write a summary, stored as type `summary` with tag `auto-summary`. It skips sessions with fewer than 3 user turns or where a summary was already stored manually. Logs go to `summarize.log` next to the database.

### Project derivation

The hooks derive the project name from the hook's `cwd`:

1. Longest **path-component** prefix match against `~/.config/memory-mcp/projects.json` (override the location with `MEMORY_MCP_PROJECTS`). The mapped value is lowercased.
2. Otherwise, if `cwd` is `$HOME`, the project is `home`.
3. Otherwise, the lowercased basename of `cwd`.

```json
{"prefixes": {"~/work/my-app": "my-app", "~/work/my-app-tools": "my-app"}}
```

Paths have `~` expanded, are cleaned, and have symlinks resolved; `~/work/my-app2` does **not** match `~/work/my-app`. A missing file falls back to the basename; an invalid file prints a warning to stderr and falls back too. The mapping is useful for monorepos or multi-repo workspaces whose subdirectories should share one project.

### Optional: Obsidian note hints

When `cwd` is not in the prefix map (i.e. `home` or an unregistered directory), `recall --hook` also matches prompt terms against Markdown file and folder names in an Obsidian vault and appends up to 2 note paths. Set the vault with `MEMORY_MCP_OB`; if the directory does not exist, this step is skipped.

## CLI usage

```bash
# Store
memory-mcp store -t til "SQLite trigram tokenizer enables CJK full-text search"
memory-mcp store -t feedback --project global "Prefer table-driven tests"
memory-mcp store -t knowledge --project my-app --tags auth,redis "Sessions live in Redis, TTL 24h"

# Search (3+ characters; CJK works too)
memory-mcp search "trigram"
memory-mcp search "全文搜索"
memory-mcp search --compact --type til -n 10 "redis"

# Browse
memory-mcp list --type summary --since 7d -n 20
memory-mcp get 12 34
memory-mcp timeline 12 --before 3 --after 3
memory-mcp context --project my-app

# Manage
memory-mcp update 12 "corrected content"
memory-mcp delete 12
memory-mcp stats

# Backup
memory-mcp export > backup.json
memory-mcp import backup.json

# Servers
memory-mcp serve                          # MCP over stdio
memory-mcp serve --http 127.0.0.1:8766    # MCP over Streamable HTTP
memory-mcp serve --http-api 127.0.0.1:8766 # REST API for --remote clients
```

Global flags: `--json` (JSON output), `--db <path>`, `--remote <url>`.

### Remote mode

Run the REST API on the machine that owns the database, then point other machines at it (e.g. through an SSH tunnel):

```bash
# central host
memory-mcp serve --http-api 127.0.0.1:8766

# client
export MEMORY_MCP_REMOTE=http://127.0.0.1:8766
memory-mcp search "trigram"
```

Clients do not need Ollama; embeddings are computed on the central host. `reindex` and `recall --hook` always operate on the local database. The REST API has no authentication, so bind it to localhost or a private network.

### Instructions for agents without MCP

For agents that only run shell commands, add something like this to `AGENTS.md` (or equivalent):

```markdown
# Cross-session memory
Store: memory-mcp store -t <feedback|til|summary|knowledge> [--tags t1,t2] [--project name] "content"
Recall: memory-mcp search "keyword"   |   memory-mcp list --since 7d
```

## Semantic search (optional)

If a local [Ollama](https://ollama.com) is running, memories are embedded on write and `search` (CLI and MCP) merges FTS5 and vector rankings with Reciprocal Rank Fusion. If Ollama is unreachable, search falls back to FTS5 only.

```bash
ollama pull bge-m3      # default embedding model
memory-mcp reindex      # backfill embeddings for existing memories; safe to re-run
```

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `--db` / `MEMORY_MCP_DB` | `~/.local/share/memory-mcp/memory.db` | Database path (the flag wins). |
| `--remote` / `MEMORY_MCP_REMOTE` | — | REST API URL of a central memory-mcp; overrides `--db`. |
| `MEMORY_MCP_OLLAMA_URL` | `http://localhost:11434` | Ollama base URL. |
| `MEMORY_MCP_EMBED_MODEL` | `bge-m3` | Embedding model. |
| `MEMORY_MCP_PROJECTS` | `~/.config/memory-mcp/projects.json` | Path-prefix → project mapping for hooks. |
| `MEMORY_MCP_OB` | `$HOME/ob` | Obsidian vault for `recall --hook` note hints (skipped if missing). |

## Development

```bash
make build   # go build -o bin/memory-mcp . (version from git describe)
make test    # go test ./...
make lint    # go vet ./...
```

## License

MIT — see [LICENSE](LICENSE).
