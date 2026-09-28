package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	in := Read(strings.NewReader(`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/home/dev/work/Globex","reason":"exit"}`))
	if in.SessionID != "s1" || in.TranscriptPath != "/t.jsonl" || in.Cwd != "/home/dev/work/Globex" || in.Reason != "exit" {
		t.Errorf("got %+v", in)
	}
}

func TestReadEmptyInput(t *testing.T) {
	if in := Read(strings.NewReader("")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
	if in := Read(strings.NewReader("not json")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
}

func TestProject(t *testing.T) {
	if got := Project("/home/dev/work/Globex"); got != "globex" {
		t.Errorf("got %q", got)
	}
	if got := Project("/home/dev/side/my.blog"); got != "my.blog" {
		t.Errorf("got %q", got)
	}
	wd, _ := os.Getwd()
	if got := Project(""); got != strings.ToLower(filepath.Base(wd)) {
		t.Errorf("got %q", got)
	}
}
