// Package summarize 在 session 結束時把 Claude Code transcript 摘要成 summary 記憶。
package summarize

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// Transcript 從 JSONL 抽出的對話文字。
type Transcript struct {
	Start     time.Time
	UserTurns int
	Text      string
}

type transcriptLine struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseTranscript 只保留 user／assistant 的文字（排除 tool、thinking、meta、sidechain），超過 maxRunes 時保留尾端。
func ParseTranscript(r io.Reader, maxRunes int) (*Transcript, error) {
	br := bufio.NewReader(r)
	tr := &Transcript{}
	var parts []string
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			var l transcriptLine
			if json.Unmarshal(raw, &l) == nil {
				if tr.Start.IsZero() && l.Timestamp != "" {
					if ts, perr := time.Parse(time.RFC3339Nano, l.Timestamp); perr == nil {
						tr.Start = ts.UTC()
					}
				}
				if text := lineText(l); text != "" {
					parts = append(parts, "["+l.Type+"] "+text)
					if l.Type == "user" {
						tr.UserTurns++
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	text := []rune(strings.Join(parts, "\n\n"))
	if len(text) > maxRunes {
		text = text[len(text)-maxRunes:]
	}
	tr.Text = string(text)
	return tr, nil
}

func lineText(l transcriptLine) string {
	if (l.Type != "user" && l.Type != "assistant") || l.IsMeta || l.IsSidechain || len(l.Message.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []contentBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			texts = append(texts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(texts, "\n")
}
