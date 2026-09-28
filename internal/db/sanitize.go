package db

import (
	"errors"
	"regexp"
	"strings"
)

// RedactedMark 取代敏感內容的標記。
const RedactedMark = "[REDACTED]"

// ErrEmptyAfterSanitize 過濾後內容為空，不寫入。
var ErrEmptyAfterSanitize = errors.New("content empty after sanitize")

var privateBlock = regexp.MustCompile(`(?is)<private>.*?</private>`)

// secretPatterns 順序有意義：sk-ant- 必須在 sk- 之前。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
}

var keyValue = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(\s*[:=]\s*["']?)([^\s"',;/][^\s"',;]{5,})`)

// Sanitize 刪除 <private> 區段並遮蔽常見金鑰格式，回傳過濾後內容（已 TrimSpace）與處理數。
func Sanitize(content string) (string, int) {
	n := 0
	out := privateBlock.ReplaceAllStringFunc(content, func(string) string {
		n++
		return ""
	})
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(string) string {
			n++
			return RedactedMark
		})
	}
	out = keyValue.ReplaceAllStringFunc(out, func(m string) string {
		sub := keyValue.FindStringSubmatch(m)
		if strings.HasPrefix(sub[3], RedactedMark) {
			return m
		}
		n++
		return sub[1] + sub[2] + RedactedMark
	})
	return strings.TrimSpace(out), n
}
