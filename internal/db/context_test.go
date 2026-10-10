package db

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectContextSections(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "global pref"})
	d.Store(&Memory{Type: "feedback", Content: "global tagged", Project: "global"})
	d.Store(&Memory{Type: "feedback", Content: "other project pref", Project: "widget"})
	for i := 0; i < 4; i++ {
		d.Store(&Memory{Type: "summary", Content: "sum", Project: "globex"})
	}
	d.Store(&Memory{Type: "til", Content: "globex til", Project: "globex"})
	d.Store(&Memory{Type: "til", Content: "widget til", Project: "widget"})

	out, err := d.Context(ContextOptions{Project: "globex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### 全域 feedback", "global tagged", "### globex summary", "### globex 其他記憶", "globex til", "memory_get", "tokens)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, bad := range []string{"global pref", "other project pref", "widget til"} {
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

// sectionLines 回傳指定段標題下的記憶行數，段落不存在回 -1。
func sectionLines(out, title string) int {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if l != title {
			continue
		}
		n := 0
		for _, x := range lines[i+1:] {
			if !strings.HasPrefix(x, "- #") {
				break
			}
			n++
		}
		return n
	}
	return -1
}

func TestProjectContextEmptyProjectFeedbackNotGlobal(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "english lesson note"})
	d.Store(&Memory{Type: "feedback", Content: "real global rule", Project: "global"})
	d.Store(&Memory{Type: "til", Content: "globex til", Project: "globex"})

	out, err := d.Context(ContextOptions{Project: "globex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "real global rule") {
		t.Errorf("missing global rule:\n%s", out)
	}
	if strings.Contains(out, "english lesson note") {
		t.Errorf("empty-project feedback leaked into context:\n%s", out)
	}
}

func TestProjectContextExcludesPersonal(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "real global rule", Project: "global"})
	d.Store(&Memory{Type: "feedback", Content: "personal feedback", Project: "personal-x"})
	d.Store(&Memory{Type: "summary", Content: "personal summary", Project: "personal-x"})
	d.Store(&Memory{Type: "til", Content: "personal til", Project: "personal-x"})

	for _, project := range []string{"globex", "personal-x"} {
		out, err := d.Context(ContextOptions{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"personal feedback", "personal summary", "personal til"} {
			if strings.Contains(out, bad) {
				t.Errorf("project %s: unexpected %q in:\n%s", project, bad, out)
			}
		}
	}
}

func TestProjectContextFeedbackSeparateFromOthers(t *testing.T) {
	d := testDB(t)
	for i := 0; i < 25; i++ {
		d.Store(&Memory{Type: "feedback", Content: fmt.Sprintf("fb-%02d", i), Project: "p"})
	}
	for i := 0; i < 15; i++ {
		d.Store(&Memory{Type: "til", Content: fmt.Sprintf("til-%02d", i), Project: "p"})
	}

	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if n := sectionLines(out, "### p feedback"); n != 20 {
		t.Errorf("feedback lines = %d, want 20:\n%s", n, out)
	}
	if n := sectionLines(out, "### p 其他記憶"); n != 10 {
		t.Errorf("other lines = %d, want 10:\n%s", n, out)
	}
	if strings.Contains(out, "fb-04") || !strings.Contains(out, "fb-24") || !strings.Contains(out, "fb-05") {
		t.Errorf("feedback should be the latest 20 (fb-05..fb-24):\n%s", out)
	}
	fbIdx, sumIdx := strings.Index(out, "### p feedback"), strings.Index(out, "### p 其他記憶")
	if fbIdx < 0 || sumIdx < 0 || fbIdx > sumIdx {
		t.Errorf("feedback section should come before others:\n%s", out)
	}
}

func TestProjectContextBudgetTrimsOthersFirst(t *testing.T) {
	d := testDB(t)
	long := strings.Repeat("長", 150)
	for i := 0; i < 10; i++ {
		d.Store(&Memory{Type: "feedback", Content: "g" + long, Project: "global"})
	}
	for i := 0; i < 20; i++ {
		d.Store(&Memory{Type: "feedback", Content: "f" + long, Project: "p"})
	}
	for i := 0; i < 3; i++ {
		d.Store(&Memory{Type: "summary", Content: "s" + long, Project: "p"})
	}
	for i := 0; i < 10; i++ {
		d.Store(&Memory{Type: "knowledge", Content: "k" + long, Project: "p"})
	}

	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if n := sectionLines(out, "### 全域 feedback"); n != 10 {
		t.Errorf("global feedback lines = %d, want 10:\n%s", n, out)
	}
	if n := sectionLines(out, "### p feedback"); n != 20 {
		t.Errorf("project feedback lines = %d, want 20:\n%s", n, out)
	}
	if n := sectionLines(out, "### p summary"); n != 3 {
		t.Errorf("summary lines = %d, want 3:\n%s", n, out)
	}
	if n := sectionLines(out, "### p 其他記憶"); n >= 10 {
		t.Errorf("other lines = %d, want trimmed below 10:\n%s", n, out)
	}
}

func TestProjectContextHomeOnlyGlobal(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "real global rule", Project: "global"})
	d.Store(&Memory{Type: "feedback", Content: "home feedback", Project: "home"})
	d.Store(&Memory{Type: "summary", Content: "home summary", Project: "home"})
	d.Store(&Memory{Type: "til", Content: "home til", Project: "home"})

	out, err := d.Context(ContextOptions{Project: "home"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "real global rule") {
		t.Errorf("missing global rule:\n%s", out)
	}
	for _, bad := range []string{"home feedback", "home summary", "home til", "### home"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, out)
		}
	}

	// 對照：一般 project 仍注入專案段
	d.Store(&Memory{Type: "til", Content: "globex til", Project: "globex"})
	out, _ = d.Context(ContextOptions{Project: "globex"})
	if !strings.Contains(out, "globex til") {
		t.Errorf("non-home project should keep project sections:\n%s", out)
	}
}
