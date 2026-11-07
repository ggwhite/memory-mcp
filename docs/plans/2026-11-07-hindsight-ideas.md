# Plan: learn from Hindsight (optional LLM, observations, mental models, temporal search)

Branch: `feat-hindsight-ideas` (worktree `.worktrees/feat-hindsight-ideas`).
Baseline: `go test ./...` = 238 passed in 10 packages.

## Principles (apply to every task)

- **No LLM configured ⇒ behaviour is identical to today.** Every LLM feature degrades to a non-LLM path or a clear "LLM not configured" message; nothing crashes, no hook slows down.
- **Hook hot path stays DB-only.** `recall --hook` (300 ms budget) and `context --hook` never call an LLM or the network. LLM output is materialized into tables that hooks read.
- **Writes never wait for an LLM.** `Store` / `storeReplacing` stay LLM-free.
- **Paid providers are explicit opt-in.** A provider that bills an API key is used only when `MEMORY_MCP_LLM_PROVIDER` names it. An `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` that merely exists in the env never triggers billing by itself.
- **LLM-made content is auditable and reversible.** Provenance is recorded; evidence rows are hidden, never deleted.
- Remote mode (`--remote`): new commands that need the local DB refuse `--remote`, same as `compact`.
- Tests: every new behaviour has a unit test; network providers are tested with `httptest`; LLM-using features are tested with a fake `llm.Client`. No test touches the real network or the real `claude` binary.

## T1 — `internal/llm` provider layer + `source` provenance column

Package `internal/llm` (must NOT import `internal/db`, to avoid an import cycle):

```go
type Tier int // TierCheap (classify/dedup), TierSynth (summaries, mental models)
type Request struct { System, Prompt string; Tier Tier; MaxTokens int }
type Client interface {
    Complete(ctx context.Context, req Request) (string, error)
    Name() string // e.g. "anthropic/claude-haiku-5-5", "openai/gpt-x", "claude-cli/haiku"
}
func CompleteJSON(ctx context.Context, c Client, req Request, out any) error
// portable: asks for JSON only, strips ``` fences, json.Unmarshal, on failure retries ONCE
// with the parse error appended to the prompt.
```

Providers:
- `anthropic`: plain `net/http` POST `{base}/v1/messages` (default base `https://api.anthropic.com`), headers `x-api-key`, `anthropic-version: 2023-06-01`, body `{model, max_tokens, system, messages:[{role:"user",content}]}`; concatenate `content[].text`. Non-2xx → error including status; `stop_reason == "refusal"` → error.
- `openai` (OpenAI-compatible): POST `{base}/chat/completions` (default base `https://api.openai.com/v1`; Ollama = `http://localhost:11434/v1`, key optional for local), `Authorization: Bearer`, body `{model, max_tokens, messages:[system,user]}`; read `choices[0].message.content`.
- `claude-cli`: move today's `summarize.ClaudeRunner` logic here (same args, same anti-recursion env var). Model per tier: cheap `haiku`, synth `sonnet` (overridable).
- Optional `Redact func(string) string` applied to System+Prompt before any **network** provider sends; CLI wiring passes `db.Sanitize` (wrap to drop the count).

Config — `llm.FromEnv(getenv func(string) string, lookPath func(string) (string, error)) (Client, error)` (injectable for tests):
- `MEMORY_MCP_LLM_PROVIDER` = `anthropic` | `openai` | `claude-cli` | `off`.
- `MEMORY_MCP_LLM_API_KEY`; when empty, falls back to `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` **only** for the provider explicitly named.
- `MEMORY_MCP_LLM_BASE_URL`, `MEMORY_MCP_LLM_MODEL_CHEAP`, `MEMORY_MCP_LLM_MODEL_SYNTH`. Defaults: anthropic `claude-haiku-5-5` / `claude-sonnet-5-5`; openai: model env vars required (error if missing).
- Resolution: provider set → use it (missing key for anthropic, or missing models for openai → error naming the env var). `off` → `nil, nil`. Unset → `claude` on PATH ⇒ claude-cli, else `nil, nil` (today's behaviour).
- Timeouts when ctx has no deadline: cheap 20 s, synth 90 s.
- Daily budget: `MEMORY_MCP_LLM_DAILY_CALLS` (default 50, `0` = unlimited). Implement as a wrapper `llm.WithBudget(c Client, counter Counter, limit int)` where `Counter` is an interface (`Incr(day string) (int, error)`); `internal/db` implements it with table `llm_usage(day TEXT PRIMARY KEY, calls INTEGER)`. Over budget → `llm.ErrBudget`, caller logs and skips.

Wiring:
- `summarize` and `compact` take an `llm.Client` (adapter or replace `Runner`); existing tests keep passing (convert fakes as needed). If no client resolves, SessionEnd summary logs "LLM not configured" to the existing summarize.log and exits 0.
- `source` column: `memories.source TEXT NOT NULL DEFAULT 'user'`, added by migration to existing DBs (follow existing migrate pattern in `db.go`). `Memory.Source string json:"source,omitempty"`. Summaries written by the SessionEnd worker get `source = "llm:" + client.Name()`; compact rollups get `"compact"`. REST client/server, export/import carry it (import of old exports without the field → `user`).
- New command `memory-mcp llm status`: prints resolved provider name + cheap/synth models + daily budget used/limit, or `disabled: <reason>`. Never prints the key.

Acceptance:
- `go build ./... && go vet ./... && go test ./...` all pass; the 238 existing tests still pass (adjusted only where signatures changed).
- Table tests for `FromEnv`: nothing set + no claude ⇒ nil; nothing set + claude on PATH ⇒ claude-cli; `ANTHROPIC_API_KEY` set but PROVIDER unset + no claude ⇒ nil (no billing); PROVIDER=anthropic without any key ⇒ error; PROVIDER=openai without models ⇒ error; PROVIDER=off ⇒ nil.
- `httptest` tests for anthropic + openai: success, non-2xx (429) error, refusal, timeout, `CompleteJSON` retry path (first reply invalid JSON, second valid), Redact applied to the outgoing body.
- Migration test: DB created with the old schema gains `source` with existing rows = `user`.
- Budget wrapper test: limit 2 ⇒ third call returns `ErrBudget`.

## T2 — temporal filter in search

- `SearchOptions` gains `Since, Until string`. Accept relative (`7d`, `24h`, `2w` — reuse/extend `parseSince`) and absolute `YYYY-MM-DD` (Until is inclusive of that day). Filter on `created`. Applies to FTS5 and vector branches of hybrid search.
- CLI `search --since --until`; MCP `memory_search` gets optional `since` / `until` string params (describe formats in the tool schema); REST client/server pass them through.
- Hook recall is NOT changed.
- Acceptance: tests for relative + absolute bounds, bad format → error, FTS-only and hybrid paths, MCP param pass-through, REST pass-through; full suite green.

## T3 — `consolidate`: near-duplicate memories → observations (needs T1)

- Table `observations(id INTEGER PK, project TEXT, type TEXT, content TEXT, evidence TEXT /*JSON array of memory ids*/, evidence_hash TEXT, proof_count INTEGER, source TEXT /*"llm:<name>" or "heuristic"*/, active INTEGER DEFAULT 1, created, updated)`.
- Candidate clustering (no LLM): within one (project, type) — default type `feedback` — union-find over pairs with similarity ≥ threshold. Similarity = cosine of stored embeddings when both rows have one, else Jaccard over character trigrams (CJK-safe). Default threshold 0.35 for trigram, 0.85 for cosine; flag `--threshold`. Clusters of size ≥ 2 only.
- LLM step (TierCheap, `CompleteJSON`): per cluster, ask which members state the same rule and for one canonical sentence in the dominant language of the members: `{"groups":[{"ids":[..],"canonical":"..."}]}`; only groups of ≥ 2 ids become observations; ids not in the cluster are ignored.
- No LLM: canonical = content of the newest member; `source = "heuristic"`.
- Refine, don't duplicate: if a group overlaps the evidence of an existing active observation, update that observation (union evidence, recompute proof_count = len(evidence), new canonical, `updated`). Skip groups whose evidence_hash is unchanged.
- CLI `memory-mcp consolidate [--project p|--all] [--type feedback] [--threshold x] [--apply]` — default is plan (print proposed observations, write nothing); `--apply` writes a JSON backup first (same dir/pattern as compact) then writes. `memory-mcp observation list [--project p]` and `observation delete <id>` (sets active = 0 → evidence shows again).
- `projectContext` (`internal/db/db.go:532`): new sections for active observations of `global` and of the project, rendered first, line format `- o<id> ×<proof_count> <canonical> (ev: #a #b …)`, snippet-trimmed like other lines; memory ids that are evidence of an active observation are dropped from the raw feedback sections. With no observations the output is byte-identical to today.
- `--remote` refused.
- Acceptance: tests for trigram similarity (incl. CJK), clustering, fake-LLM grouping (partial groups, foreign ids ignored), heuristic fallback, refine path, unchanged-hash skip, plan writes nothing, apply writes backup, delete restores evidence lines in context, context unchanged when table empty; full suite green.

## T4 — mental models (needs T1, T3)

- Table `mental_models(id INTEGER PK, project TEXT, question TEXT, answer TEXT, source_ids TEXT /*JSON*/, built_from_max_id INTEGER, source TEXT, created, updated)`.
- CLI `memory-mcp mental add --project p "<question>"`, `mental list [--project p]`, `mental rm <id>`, `mental build [--project p|--all] [--force]`.
- Build (TierSynth): input = memories of the project selected by FTS/hybrid search on the question (top 20) plus the 5 newest summaries, plus active observations of the project; dedup; cap ~12k runes. Prompt asks for a concise answer (≤ 12 lines, same language as the memories) that cites memory ids as `#id`. Store answer, source_ids, `built_from_max_id = max(memories.id where project = p)`. Skip when that max id has not grown since the last build, unless `--force`. No LLM ⇒ `build` prints "LLM not configured" and exits 0; add/list/rm still work.
- `projectContext`: a `### <project> mental models` section placed after the global observations/feedback and before project feedback; each model renders as `**Q:** question` + answer lines; trimmed by `fitBudget` like other sections. No models ⇒ output identical to before.
- SessionEnd worker: after it stores the summary, if an LLM client resolved and the project has mental models, run the incremental build for that project (still in the detached worker; failures only logged).
- Acceptance: tests with fake LLM for build/skip/force, context rendering + budget trimming, no-LLM path, worker trigger; full suite green.

## Done

- README: new sections "Optional LLM", "Consolidate & observations", "Mental models", search `--since/--until`; example launchd/cron line for a nightly `consolidate --all --apply && mental build --all`.
- `make build`, `make lint`, `make test` green.
