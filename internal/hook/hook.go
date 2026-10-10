// Package hook 讀取 Claude Code hook 的 stdin JSON 並推算 project 名稱。
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SummarizingEnv 自動 summary 呼叫 claude -p 時設定，讓 hook 跳過避免遞迴。
const SummarizingEnv = "MEMORY_MCP_SUMMARIZING"

// Input Claude Code hook 傳入的欄位（只取用到的）。
type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Reason         string `json:"reason"`
	Source         string `json:"source"`
	HookEventName  string `json:"hook_event_name"`
	Prompt         string `json:"prompt"`
}

// Read 解析 hook JSON，失敗回零值。
func Read(r io.Reader) Input {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Input{}
	}
	return in
}

// ReadStdin 讀 stdin 的 hook JSON；stdin 是終端機時不讀，直接回零值。
func ReadStdin() Input {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return Input{}
	}
	return Read(os.Stdin)
}

// ProjectsEnv 覆寫路徑前綴對照設定檔位置。
const ProjectsEnv = "MEMORY_MCP_PROJECTS"

type projectsConfig struct {
	Prefixes map[string]string `json:"prefixes"`
}

// Project 推算 project：先以設定檔的路徑前綴做最長比對，沒命中時 $HOME 回 home，
// 其餘用工作目錄 basename 小寫；cwd 為空時用目前目錄。
func Project(cwd string) string {
	p, _ := Resolve(cwd)
	return p
}

// Resolve 同 Project，另回傳是否命中前綴對照表（即登記過的工作專案）。
func Resolve(cwd string) (string, bool) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	home, _ := os.UserHomeDir()
	resolved := resolvePath(cwd, home)

	best, bestLen := "", -1
	for prefix, name := range loadPrefixes(home) {
		p := resolvePath(prefix, home)
		if hasPathPrefix(resolved, p) && len(p) > bestLen {
			best, bestLen = name, len(p)
		}
	}
	if bestLen >= 0 {
		return strings.ToLower(best), true
	}
	if home != "" && resolved == resolvePath(home, home) {
		return "home", false
	}
	return strings.ToLower(filepath.Base(cwd)), false
}

// loadPrefixes 讀前綴對照表；檔案不存在回 nil，格式錯誤往 stderr 警告後回 nil。
func loadPrefixes(home string) map[string]string {
	path := os.Getenv(ProjectsEnv)
	if path == "" {
		if home == "" {
			return nil
		}
		path = filepath.Join(home, ".config", "memory-mcp", "projects.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg projectsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "memory-mcp: ignore invalid %s: %v\n", path, err)
		return nil
	}
	return cfg.Prefixes
}

// resolvePath 展開 ~、Clean，並解 symlink（失敗就用 Clean 後的值）。
func resolvePath(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		p = filepath.Join(home, p[1:])
	}
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// hasPathPrefix 以路徑元件為邊界判斷 p 是否在 prefix 之下（含相等）。
func hasPathPrefix(p, prefix string) bool {
	if p == prefix {
		return true
	}
	if prefix == string(filepath.Separator) {
		return strings.HasPrefix(p, prefix)
	}
	return strings.HasPrefix(p, prefix+string(filepath.Separator))
}
