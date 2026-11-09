# Render from the repo root: assets/render.sh demo-remote (vhs -> mp4 -> animated WebP).
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
# Ollama is pointed at a closed port so results are FTS5 only and reproducible.
# Port 8799 is used so a real server on the default 8766 is never touched;
# a stale demo server is killed first, and an EXIT/HUP trap stops it with the recording shell.
Output "/tmp/memory-mcp-vhs/demo-remote.mp4"

Require memory-mcp

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 360
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 22ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-remote.db MEMORY_MCP_OLLAMA_URL=http://127.0.0.1:9; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB`
Enter
Type `pkill -f 'serve --http-api 127.0.0.1:8799'; trap "pkill -f 'serve --http-api 127.0.0.1:8799'" EXIT HUP`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type "# host: serve the REST API"
Sleep 100ms
Enter
Sleep 200ms
Type "(memory-mcp serve --http-api 127.0.0.1:8799 2>/dev/null &)"
Sleep 150ms
Enter
Sleep 600ms
Type "# client: point the CLI at the host"
Sleep 100ms
Enter
Sleep 200ms
Type "export MEMORY_MCP_REMOTE=http://127.0.0.1:8799"
Sleep 150ms
Enter
Sleep 400ms
Type `memory-mcp store -t til --project my-app "From my laptop"`
Sleep 150ms
Enter
Sleep 1s
Type "memory-mcp stats"
Sleep 150ms
Enter
Sleep 2.5s
