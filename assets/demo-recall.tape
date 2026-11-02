# Run from the repo root: vhs assets/demo-recall.tape
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
Output assets/demo-recall.gif

Require memory-mcp
Require sqlite3

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 470
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 35ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-recall.db MEMORY_MCP_OB=/tmp/memory-mcp-no-vault MEMORY_MCP_PROJECTS=/tmp/memory-mcp-no-projects.json; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB "${TMPDIR:-/tmp}/memory-mcp/recall-demo.json"; mkdir -p /tmp/memory-mcp-recall && cd /tmp/memory-mcp-recall && memory-mcp stats >/dev/null`
Enter
Type `sqlite3 $MEMORY_MCP_DB "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<40) INSERT INTO memories(type,content,project) SELECT 'til','Note '||i||' on build pipelines, logging, metrics and deployments','misc' FROM n"`
Enter
Type `memory-mcp store -t til --project my-app --tags redis 'Redis session TTL bug: EXPIRE resets the TTL on every write; use SET with KEEPTTL to keep session expiry' >/dev/null`
Enter
Type `memory-mcp store -t knowledge --project my-app --tags auth 'Sessions live in Redis with a 24h TTL; every write to the session must keep the TTL' >/dev/null`
Enter
Type `memory-mcp store -t feedback --project global 'Prefer table-driven tests for Go code' >/dev/null`
Enter
Type `printf '{\n  "session_id": "demo",\n  "cwd": "/Users/me/work/my-app",\n  "prompt": "redis session expiry resets on every write, how do I keep the TTL?"\n}\n' > prompt.json`
Enter
Type `echo '{}' | memory-mcp recall --hook`
Enter
Type `export TIMEFORMAT='(took %3Rs)' PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 2s
Show

Type "# Claude Code sends this JSON on every UserPromptSubmit"
Enter
Sleep 400ms
Type "cat prompt.json"
Enter
Sleep 2s

Type "time memory-mcp recall --hook < prompt.json"
Sleep 300ms
Enter
Sleep 4s

Type "# Same session again: already-injected memories are not repeated"
Enter
Sleep 400ms
Type `memory-mcp recall --hook < prompt.json; echo "(nothing injected, exit $?)"`
Sleep 300ms
Enter
Sleep 3.5s
