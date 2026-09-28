package db

import (
	"testing"
	"time"
)

func TestSnippet(t *testing.T) {
	if got := Snippet("a\n b\t c", 10); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := Snippet("一二三四五", 3); got != "一二三…" {
		t.Errorf("got %q", got)
	}
	if got := Snippet("一二三", 3); got != "一二三" {
		t.Errorf("got %q", got)
	}
}

func TestCompactLine(t *testing.T) {
	m := Memory{ID: 7, Type: "til", Project: "kairos", Content: "hello world",
		Created: time.Date(2026, 9, 27, 1, 0, 0, 0, time.UTC)}
	if got := CompactLine(m, 5, true); got != "- #7 til kairos 2026-09-27 hello…" {
		t.Errorf("got %q", got)
	}
	if got := CompactLine(m, 80, false); got != "- #7 til 2026-09-27 hello world" {
		t.Errorf("got %q", got)
	}
	m.Project = ""
	if got := CompactLine(m, 80, true); got != "- #7 til 2026-09-27 hello world" {
		t.Errorf("got %q", got)
	}
}

func TestFormatFull(t *testing.T) {
	m := Memory{ID: 3, Type: "feedback", Tags: "a,b", Project: "p", Content: "body",
		Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	want := "#3 [feedback] 2026-01-02  tags:a,b  project:p\n  body"
	if got := FormatFull(m); got != want {
		t.Errorf("got %q", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("中文ab"); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestParseIDs(t *testing.T) {
	ids, err := ParseIDs("1030, 1027 5")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1030 || ids[1] != 1027 || ids[2] != 5 {
		t.Errorf("got %v", ids)
	}
	if _, err := ParseIDs("1,x"); err == nil {
		t.Error("expected error for non-numeric id")
	}
}
