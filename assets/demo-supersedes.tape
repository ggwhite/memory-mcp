# Render from the repo root: assets/render.sh demo-supersedes (vhs -> mp4 -> animated WebP).
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
# Ollama is pointed at a closed port so results are FTS5 only and reproducible.
# The "embed skipped" stderr notes this causes are filtered out of the frames.
Output "/tmp/memory-mcp-vhs/demo-supersedes.mp4"

Require memory-mcp

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 300
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 25ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-supersedes.db MEMORY_MCP_OLLAMA_URL=http://127.0.0.1:9; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB && memory-mcp stats >/dev/null`
Enter
Type `memory-mcp() { command memory-mcp "$@" 2>>/tmp/memory-mcp-demo.err; }`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type `memory-mcp store -t feedback --project global "Mock the database in integration tests"`
Sleep 200ms
Enter
Sleep 700ms
Type `memory-mcp store -t feedback --project global --supersedes 1 "Use a real DB in integration tests"`
Sleep 200ms
Enter
Sleep 1.2s
Type "memory-mcp list --type feedback"
Sleep 200ms
Enter
Sleep 2.5s
