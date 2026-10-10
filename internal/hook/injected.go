package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const maxSessionIDLen = 128

// InjectedPath 同一 session 已注入記憶 id 的狀態檔路徑；session_id 為空回空字串。
// session_id 只保留英數、-、_，其餘換成 _，避免路徑穿越。
func InjectedPath(sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, sessionID)
	if safe == "" {
		return ""
	}
	if len(safe) > maxSessionIDLen {
		safe = safe[:maxSessionIDLen]
	}
	return filepath.Join(os.TempDir(), "memory-mcp", "recall-"+safe+".json")
}

type injectedState struct {
	IDs   []int64  `json:"ids"`
	Notes []string `json:"notes,omitempty"`
}

func loadState(sessionID string) injectedState {
	var st injectedState
	path := InjectedPath(sessionID)
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if json.Unmarshal(data, &st) != nil {
		return injectedState{}
	}
	return st
}

// LoadInjected 讀取 session 已注入的記憶 id；檔案不存在或壞掉回空集合。
func LoadInjected(sessionID string) map[int64]bool {
	out := make(map[int64]bool)
	for _, id := range loadState(sessionID).IDs {
		out[id] = true
	}
	return out
}

// LoadInjectedNotes 讀取 session 已注入的筆記路徑。
func LoadInjectedNotes(sessionID string) map[string]bool {
	out := make(map[string]bool)
	for _, n := range loadState(sessionID).Notes {
		out[n] = true
	}
	return out
}

// AddInjected 把記憶 id 併入 session 狀態檔；session_id 為空時不做事。
func AddInjected(sessionID string, ids []int64) error {
	return addInjected(sessionID, ids, nil)
}

// AddInjectedNotes 把筆記路徑併入 session 狀態檔；session_id 為空時不做事。
func AddInjectedNotes(sessionID string, notes []string) error {
	return addInjected(sessionID, nil, notes)
}

// addInjected 合併後先寫暫存檔再 rename。
func addInjected(sessionID string, ids []int64, notes []string) error {
	path := InjectedPath(sessionID)
	if path == "" || len(ids)+len(notes) == 0 {
		return nil
	}
	st := loadState(sessionID)
	seenID := make(map[int64]bool)
	for _, id := range st.IDs {
		seenID[id] = true
	}
	for _, id := range ids {
		if !seenID[id] {
			seenID[id] = true
			st.IDs = append(st.IDs, id)
		}
	}
	seenNote := make(map[string]bool)
	for _, n := range st.Notes {
		seenNote[n] = true
	}
	for _, n := range notes {
		if !seenNote[n] {
			seenNote[n] = true
			st.Notes = append(st.Notes, n)
		}
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".recall-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
