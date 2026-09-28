// Package hook 讀取 Claude Code hook 的 stdin JSON 並推算 project 名稱。
package hook

import (
	"encoding/json"
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

// Project 以工作目錄 basename 小寫作為 project；cwd 為空時用目前目錄。
func Project(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return strings.ToLower(filepath.Base(cwd))
}
