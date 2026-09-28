package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	in := Read(strings.NewReader(`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/Users/white/Tyche/Kairos","reason":"exit"}`))
	if in.SessionID != "s1" || in.TranscriptPath != "/t.jsonl" || in.Cwd != "/Users/white/Tyche/Kairos" || in.Reason != "exit" {
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
	if got := Project("/Users/white/Tyche/Kairos"); got != "kairos" {
		t.Errorf("got %q", got)
	}
	if got := Project("/Users/white/Documents/ggw.studio"); got != "ggw.studio" {
		t.Errorf("got %q", got)
	}
	wd, _ := os.Getwd()
	if got := Project(""); got != strings.ToLower(filepath.Base(wd)) {
		t.Errorf("got %q", got)
	}
}
