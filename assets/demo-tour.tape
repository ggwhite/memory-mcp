# Hero tour. Render with assets/render.sh demo-tour (vhs -> mp4 -> animated WebP).
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
# Ollama is pointed at a closed port so results are FTS5 only and reproducible;
# the resulting "embed skipped" stderr notes are filtered out of the frames.
Output "/tmp/memory-mcp-vhs/demo-tour.mp4"

Require memory-mcp
Require sqlite3

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 480
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 30ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-tour.db MEMORY_MCP_OLLAMA_URL=http://127.0.0.1:9 MEMORY_MCP_OB=/tmp/memory-mcp-no-vault MEMORY_MCP_PROJECTS=/tmp/memory-mcp-no-projects.json; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB "${TMPDIR:-/tmp}/memory-mcp/recall-tour.json"; mkdir -p /tmp/memory-mcp-tour && cd /tmp/memory-mcp-tour && command memory-mcp stats >/dev/null`
Enter
Type `sqlite3 $MEMORY_MCP_DB "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<40) INSERT INTO memories(type,content,project,created,updated) SELECT 'til','Note '||i||' on build pipelines, logging, metrics and deployments','misc','2026-09-01T10:00:00','2026-09-01T10:00:00' FROM n"`
Enter
Type `memory-mcp() { command memory-mcp "$@" 2>>/tmp/memory-mcp-tour.err; }`
Enter
Type `printf '{"session_id": "tour", "cwd": "/Users/me/work/my-app",\n "prompt": "redis session expiry resets on every write, how do I keep the TTL?"}\n' > prompt.json`
Enter
Type `echo '{}' | memory-mcp recall --hook`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type "# 1. The agent stores what it learned"
Enter
Type@25ms `memory-mcp store -t til --project my-app "EXPIRE resets the TTL on writes; SET ... KEEPTTL keeps it"`
Sleep 200ms
Enter
Sleep 800ms
Type@25ms `memory-mcp store -t feedback --project global "Prefer table-driven tests for Go code"`
Sleep 200ms
Enter
Sleep 1.2s

Type "# 2. A later session asks; the UserPromptSubmit hook injects the memory"
Enter
Type "memory-mcp recall --hook < prompt.json"
Sleep 200ms
Enter
Sleep 3s

Type "# 3. Or search on demand, then fetch full content by id"
Enter
Type "memory-mcp search --compact KEEPTTL"
Sleep 200ms
Enter
Sleep 1.5s
Type "memory-mcp get 41"
Sleep 200ms
Enter
Sleep 3s
