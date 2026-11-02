# Run from the repo root: vhs assets/demo-compact.tape
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
# Dry run only: --apply would call claude -p.
Output assets/demo-compact.gif

Require memory-mcp
Require sqlite3

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 520
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 35ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-compact.db; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB && memory-mcp stats >/dev/null`
Enter
Type `sqlite3 $MEMORY_MCP_DB "INSERT INTO memories(type,content,project,tags,created,updated) VALUES ('summary','Added FTS5 trigram search; CJK queries need 3+ chars','my-app','auto-summary','2026-05-04T10:00:00','2026-05-04T10:00:00'), ('summary','Fixed Redis session TTL bug with SET KEEPTTL','my-app','auto-summary','2026-05-12T10:00:00','2026-05-12T10:00:00'), ('summary','Wired hooks: context, recall, summarize','my-app','auto-summary','2026-05-27T10:00:00','2026-05-27T10:00:00'), ('summary','Release pipeline with goreleaser','my-app','auto-summary','2026-06-03T10:00:00','2026-06-03T10:00:00'), ('summary','Benchmarked recall latency under 300 ms','my-app','auto-summary','2026-06-18T10:00:00','2026-06-18T10:00:00'), ('summary','Drafted API docs','docs-site','auto-summary','2026-06-09T10:00:00','2026-06-09T10:00:00')"`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type "# Months of auto-summaries pile up..."
Enter
Sleep 400ms
Type "memory-mcp list --type summary -n 3"
Sleep 300ms
Enter
Sleep 2.5s

Type "# Dry run: group summaries older than 60d by project + month"
Enter
Sleep 400ms
Type "memory-mcp compact"
Sleep 300ms
Enter
Sleep 3.5s

Type "# --apply backs up, merges each group with claude -p, deletes the originals"
Enter
Sleep 3s
