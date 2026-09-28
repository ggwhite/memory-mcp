package db

import (
	"fmt"
	"strconv"
	"strings"
)

// Snippet 把空白壓成單一空格，取前 n 個 rune，超過加「…」。
func Snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// CompactLine 一筆記憶的單行索引格式。
func CompactLine(m Memory, n int, withProject bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- #%d %s ", m.ID, m.Type)
	if withProject && m.Project != "" {
		b.WriteString(m.Project + " ")
	}
	b.WriteString(m.Created.Format("2006-01-02") + " ")
	b.WriteString(Snippet(m.Content, n))
	return b.String()
}

// FormatFull 一筆記憶的全文格式。
func FormatFull(m Memory) string {
	line := fmt.Sprintf("#%d [%s] %s", m.ID, m.Type, m.Created.Format("2006-01-02"))
	if m.Tags != "" {
		line += fmt.Sprintf("  tags:%s", m.Tags)
	}
	if m.Project != "" {
		line += fmt.Sprintf("  project:%s", m.Project)
	}
	return line + "\n  " + m.Content
}

// EstimateTokens 粗估 token 數：CJK rune 算 1，其他每 4 個 rune 算 1。
func EstimateTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if r >= 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// TimelineLines 將 timeline 結果轉成索引行，中心那筆以「→」開頭。
func TimelineLines(memories []Memory, center int64, n int) []string {
	lines := make([]string, len(memories))
	for i, m := range memories {
		line := CompactLine(m, n, false)
		if m.ID == center {
			line = "→" + strings.TrimPrefix(line, "-")
		}
		lines[i] = line
	}
	return lines
}

// ParseIDs 解析逗號或空白分隔的記憶 ID。
func ParseIDs(s string) ([]int64, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	ids := make([]int64, 0, len(fields))
	for _, f := range fields {
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q: %w", f, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
