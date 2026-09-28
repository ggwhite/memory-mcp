package db

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectContextSections(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "global pref"})
	d.Store(&Memory{Type: "feedback", Content: "global tagged", Project: "global"})
	d.Store(&Memory{Type: "feedback", Content: "other project pref", Project: "yuna"})
	for i := 0; i < 4; i++ {
		d.Store(&Memory{Type: "summary", Content: "sum", Project: "kairos"})
	}
	d.Store(&Memory{Type: "til", Content: "kairos til", Project: "kairos"})
	d.Store(&Memory{Type: "til", Content: "yuna til", Project: "yuna"})

	out, err := d.Context(ContextOptions{Project: "kairos"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### 全域 feedback", "global pref", "global tagged", "### kairos summary", "### kairos 其他記憶", "kairos til", "memory_get", "tokens)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, bad := range []string{"other project pref", "yuna til"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, out)
		}
	}
	if n := strings.Count(out, " summary "); n != 3 {
		t.Errorf("summary lines = %d, want 3", n)
	}
}

func TestProjectContextEmpty(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "x", Project: "other"})
	out, err := d.Context(ContextOptions{Project: "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("want empty, got %q", out)
	}
}

func TestFitBudget(t *testing.T) {
	header := "## h\n\n"
	sections := []contextSection{
		{"### a", []string{"a1", "a2"}},
		{"### b", []string{strings.Repeat("長", 40), strings.Repeat("長", 40)}},
	}
	body := fitBudget(header, sections, 70)
	if n := utf8.RuneCountInString(body); n > 70 {
		t.Errorf("runes = %d, want <= 70:\n%s", n, body)
	}
	if !strings.Contains(body, "a1") || !strings.Contains(body, "### b") {
		t.Errorf("should drop from the last section first:\n%s", body)
	}
	if strings.Count(body, strings.Repeat("長", 40)) != 1 {
		t.Errorf("want exactly one b line left:\n%s", body)
	}
}

func TestContextFullAndCompact(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: strings.Repeat("a", 300), Project: "p"})

	compact, _ := d.Context(ContextOptions{Limit: 5})
	if !strings.Contains(compact, "- #1 til p ") || !strings.Contains(compact, "…") {
		t.Errorf("compact = %q", compact)
	}
	full, _ := d.Context(ContextOptions{Limit: 5, Full: true})
	if !strings.Contains(full, strings.Repeat("a", 300)) {
		t.Errorf("full should contain whole content")
	}
}
