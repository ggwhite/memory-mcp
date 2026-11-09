# Render from the repo root: assets/render.sh demo-browse (vhs -> mp4 -> animated WebP).
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
# Ollama is pointed at a closed port so results are FTS5 only and reproducible.
Output "/tmp/memory-mcp-vhs/demo-browse.mp4"

Require memory-mcp
Require sqlite3

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 340
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 30ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-browse.db MEMORY_MCP_OLLAMA_URL=http://127.0.0.1:9; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB && memory-mcp stats >/dev/null`
Enter
Type `sqlite3 $MEMORY_MCP_DB "INSERT INTO memories(type,content,project,tags,created,updated) VALUES ('feedback','Prefer table-driven tests for Go code','global','testing','2026-10-02T09:00:00','2026-10-02T09:00:00'), ('feedback','Write commit messages in the imperative mood','global','git','2026-10-05T09:00:00','2026-10-05T09:00:00'), ('til','Redis MULTI does not roll back on errors; check each reply','my-app','redis','2026-09-14T10:00:00','2026-09-14T10:00:00'), ('feedback','Use a real DB in integration tests, never mocks','my-app','testing','2026-10-12T09:00:00','2026-10-12T09:00:00'), ('knowledge','Sessions live in Redis with a 24h TTL','my-app','auth,redis','2026-10-20T10:00:00','2026-10-20T10:00:00'), ('summary','Added rate limiting to the login endpoint; todo: per-IP metrics','my-app','auto-summary','2026-10-28T18:00:00','2026-10-28T18:00:00'), ('til','EXPIRE resets the TTL on every write; SET ... KEEPTTL keeps it','my-app','redis','2026-11-03T17:00:00','2026-11-03T17:00:00'), ('summary','Fixed Redis session TTL bug with SET KEEPTTL','my-app','auto-summary','2026-11-03T18:00:00','2026-11-03T18:00:00'), ('summary','Migrated config loading to env vars + defaults','my-app','auto-summary','2026-11-06T18:00:00','2026-11-06T18:00:00'), ('til','Drafted API docs with OpenAPI 3.1','docs-site','docs','2026-10-22T10:00:00','2026-10-22T10:00:00')"`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type "memory-mcp get 7"
Sleep 300ms
Enter
Sleep 1.5s
Type "memory-mcp timeline 7 --before 2 --after 2"
Sleep 300ms
Enter
Sleep 3.5s
