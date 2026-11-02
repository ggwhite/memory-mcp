Output demo.gif

Require memory-mcp

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1000
Set Height 550
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 40ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-demo.db PS1='\[\e[35m\]$\[\e[0m\] '; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB && clear`
Enter
Sleep 1s
Show

Type `echo "==> Store memories with types & tags"`
Enter
Sleep 800ms

Type@50ms `memory-mcp store -t til --tags go,sqlite "SQLite trigram tokenizer 支援 CJK 全文搜索"`
Sleep 300ms
Enter
Sleep 1s

Type@50ms `memory-mcp store -t feedback --tags testing --project myapp "Integration test 要用 real DB，不要 mock"`
Sleep 300ms
Enter
Sleep 1s

Type@50ms `memory-mcp store -t knowledge "Go 1.22+ 支援 range over func"`
Sleep 300ms
Enter
Sleep 1.5s

Type `echo "==> Full-text search (supports CJK)"`
Enter
Sleep 800ms

Type@50ms `memory-mcp search "全文搜索"`
Sleep 300ms
Enter
Sleep 2s

Type@50ms `memory-mcp search "testing"`
Sleep 300ms
Enter
Sleep 2s

Type `echo "==> List & filter"`
Enter
Sleep 800ms

Type "memory-mcp list"
Sleep 300ms
Enter
Sleep 2s

Type "memory-mcp list --type til"
Sleep 300ms
Enter
Sleep 2s

Type `echo "==> Stats"`
Enter
Sleep 800ms

Type "memory-mcp stats"
Sleep 300ms
Enter
Sleep 2s

Type `echo "==> Delete"`
Enter
Sleep 800ms

Type "memory-mcp delete 3"
Sleep 300ms
Enter
Sleep 1s

Type "memory-mcp list"
Sleep 300ms
Enter
Sleep 2s

Type `echo "==> Zero deps. SQLite FTS5. Works with any AI agent."`
Enter
Sleep 3s
