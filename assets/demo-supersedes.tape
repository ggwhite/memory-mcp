# Run from the repo root: vhs assets/demo-supersedes.tape
# Uses a throwaway DB under /tmp; never touches ~/.local/share/memory-mcp.
Output assets/demo-supersedes.gif

Require memory-mcp

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1100
Set Height 420
Set Padding 24
Set WindowBar Colorful
Set Theme {"name":"memory-mcp","black":"#484f58","red":"#ff7b72","green":"#7ee787","yellow":"#e3b341","blue":"#79c0ff","magenta":"#a196ff","cyan":"#76e3ea","white":"#b1bac4","brightBlack":"#6e7681","brightRed":"#ffa198","brightGreen":"#56d364","brightYellow":"#e3b341","brightBlue":"#a5d6ff","brightMagenta":"#c4bcff","brightCyan":"#b3f0ff","brightWhite":"#ffffff","background":"#0d1117","foreground":"#e6edf3","selection":"#1f1b3d","cursor":"#a196ff"}
Set TypingSpeed 35ms

Hide
Type `export BASH_SILENCE_DEPRECATION_WARNING=1 MEMORY_MCP_DB=/tmp/memory-mcp-supersedes.db; unset MEMORY_MCP_REMOTE; rm -f $MEMORY_MCP_DB`
Enter
Type `export PS1='\[\e[35m\]$\[\e[0m\] ' && clear`
Enter
Sleep 1s
Show

Type@45ms `memory-mcp store -t feedback --project global "Mock the database in integration tests"`
Sleep 300ms
Enter
Sleep 1.2s

Type "# The user corrects the rule: replace #1 instead of piling up a contradiction"
Enter
Sleep 400ms
Type@45ms `memory-mcp store -t feedback --project global --supersedes 1 "Use a real DB in integration tests"`
Sleep 300ms
Enter
Sleep 2s

Type "memory-mcp list --type feedback"
Sleep 300ms
Enter
Sleep 2s

Type "memory-mcp get 1"
Sleep 300ms
Enter
Sleep 3.5s
