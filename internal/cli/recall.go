package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
	"memory-mcp/internal/obnote"

	"github.com/spf13/cobra"
)

const (
	recallTimeout     = 300 * time.Millisecond
	recallMinRunes    = 4
	recallSnippetRune = 60
	recallNoteLimit   = 2
	// recallMaxPromptRunes 訊息超過這麼多字時只取頭尾各一半切詞：貼長 log 時中段只會拖慢
	// 逐筆比對，而提問可能在 log 前或後，頭尾都要留。
	recallMaxPromptRunes = 500
	// recallNoteMargin 走訪 vault 要比整體期限早這麼久放棄，好讓記憶結果仍能輸出。
	recallNoteMargin = 50 * time.Millisecond
	recallHeader     = "## 相關記憶（依本則訊息自動檢索，需要全文用 memory_get）\n"
)

var recallCmd = &cobra.Command{
	Use:   "recall",
	Short: "Inject memories relevant to the current prompt (UserPromptSubmit hook)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isHook, _ := cmd.Flags().GetBool("hook"); isHook {
			runRecallHook(os.Stdin, os.Stdout, os.Stderr, recallTimeout, openLocalDB)
			return nil
		}
		return fmt.Errorf("use --hook")
	},
}

func init() {
	recallCmd.Flags().Bool("hook", false, "Claude Code UserPromptSubmit hook mode: read hook JSON from stdin, print relevant memories, never fail")
	rootCmd.AddCommand(recallCmd)
}

// openLocalDB recall 只查本機 DB，設定了 --remote 時回錯誤；不設 embedder，不呼叫 embedding 服務。
func openLocalDB() (*db.DB, error) {
	if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
		return nil, errors.New("recall does not support --remote")
	}
	path := dbPath
	if path == "" {
		path = os.Getenv("MEMORY_MCP_DB")
	}
	if path == "" {
		path = defaultDBPath()
	}
	return db.Open(path)
}

// outcome 背景工作的結果；panic 會轉成 err。
type outcome[T any] struct {
	v   T
	err error
}

// spawn 在 goroutine 執行 fn，panic 時 recover 成錯誤，避免 hook 以 exit 2 擋住使用者訊息。
func spawn[T any](fn func() (T, error)) <-chan outcome[T] {
	ch := make(chan outcome[T], 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome[T]{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		v, err := fn()
		ch <- outcome[T]{v: v, err: err}
	}()
	return ch
}

// runRecallHook 在 timeout 內完成檢索；任何錯誤、panic 或逾時只往 stderr 寫一行，stdout 保持空白。
// 記憶（DB）與筆記（vault）平行查，DB 逾時時仍輸出期限內完成的筆記。
func runRecallHook(stdin io.Reader, stdout, stderr io.Writer, timeout time.Duration, open func() (*db.DB, error)) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "memory-mcp recall: panic: %v\n", r)
		}
	}()
	if os.Getenv(hook.SummarizingEnv) == "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	timedOut := func() { fmt.Fprintf(stderr, "memory-mcp recall: timeout after %s\n", timeout) }

	var in hook.Input
	select {
	case o := <-spawn(func() (hook.Input, error) { return parseRecallInput(stdin) }):
		if o.err != nil {
			fmt.Fprintf(stderr, "memory-mcp recall: %v\n", o.err)
			return
		}
		in = o.v
	case <-ctx.Done():
		timedOut()
		return
	}
	prompt, parts := recallPrompt(in.Prompt)
	if prompt == "" {
		return
	}
	project, registered := hook.Resolve(in.Cwd)

	memCh := spawn(func() ([]db.RecallResult, error) {
		return recallMemories(ctx, open, prompt, parts, project, registered, in.SessionID)
	})
	// 登記過的工作專案不比對 Obsidian 筆記。
	var noteCh <-chan outcome[[]string]
	if !registered {
		noteCh = spawn(func() ([]string, error) { return recallNotes(ctx, prompt, parts, in.SessionID) })
	}

	var memories []db.RecallResult
	var notes []string
	pending := 1
	if noteCh != nil {
		pending++
	}
	for pending > 0 {
		select {
		case o := <-memCh:
			memCh, pending = nil, pending-1
			if o.err != nil {
				fmt.Fprintf(stderr, "memory-mcp recall: %v\n", o.err)
			}
			memories = o.v
		case o := <-noteCh:
			noteCh, pending = nil, pending-1
			if o.err != nil {
				fmt.Fprintf(stderr, "memory-mcp recall: skip ob notes: %v\n", o.err)
			}
			notes = o.v
		case <-ctx.Done():
			timedOut()
			pending = 0
		}
	}
	if len(memories) == 0 && len(notes) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString(recallHeader)
	ids := make([]int64, len(memories))
	for i, r := range memories {
		ids[i] = r.ID
		b.WriteString(db.CompactLine(r.Memory, recallSnippetRune, true))
		b.WriteByte('\n')
	}
	for _, n := range notes {
		b.WriteString("- 筆記 " + n + "\n")
	}
	err := hook.AddInjected(in.SessionID, ids)
	if err == nil {
		err = hook.AddInjectedNotes(in.SessionID, notes)
	}
	if err != nil {
		fmt.Fprintf(stderr, "memory-mcp recall: save state: %v\n", err)
	}
	fmt.Fprint(stdout, b.String())
}

func parseRecallInput(stdin io.Reader) (hook.Input, error) {
	var in hook.Input
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return in, fmt.Errorf("parse hook input: %w", err)
	}
	return in, nil
}

// systemMarkers Claude Code 以 UserPromptSubmit 送進來的系統訊息（背景任務通知、system reminder、
// 本機指令輸出）開頭標記，內容與使用者提問無關。
var systemMarkers = []string{"<task-notification", "<system-reminder", "[SYSTEM NOTIFICATION", "<local-command-", "<command-name>"}

// userText 回傳第一個系統訊息標記之前的使用者文字；整則以標記開頭時回空字串。
func userText(raw string) string {
	cut := len(raw)
	for _, m := range systemMarkers {
		if i := strings.Index(raw, m); i >= 0 && i < cut {
			cut = i
		}
	}
	return raw[:cut]
}

// recallPrompt 剝除系統訊息區塊後去頭尾空白；太短或 / 開頭回空字串；超過 recallMaxPromptRunes 時取頭尾各半，
// 中間以空白分隔，避免頭尾黏成假的 3 字窗口，並回傳頭、尾兩段供查詢詞分配名額。
func recallPrompt(raw string) (string, []string) {
	prompt := strings.TrimSpace(userText(raw))
	if utf8.RuneCountInString(prompt) < recallMinRunes || strings.HasPrefix(prompt, "/") {
		return "", nil
	}
	r := []rune(prompt)
	if len(r) <= recallMaxPromptRunes {
		return prompt, nil
	}
	half := recallMaxPromptRunes / 2
	head, tail := string(r[:half]), string(r[len(r)-half:])
	return head + " " + tail, []string{head, tail}
}

func recallMemories(ctx context.Context, open func() (*db.DB, error), prompt string, parts []string, project string, registered bool, sessionID string) ([]db.RecallResult, error) {
	d, err := open()
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.Recall(ctx, db.RecallOptions{
		Prompt:          prompt,
		Parts:           parts,
		Project:         project,
		ExcludePersonal: registered,
		Exclude:         hook.LoadInjected(sessionID),
	})
}

// recallNotes 比對 vault 筆記檔名，回傳顯示路徑（同時是去重 key）；vault 不存在回空。
// 走訪比整體期限早 recallNoteMargin 放棄，逾時只略過筆記。
func recallNotes(ctx context.Context, prompt string, parts []string, sessionID string) ([]string, error) {
	root, display, ok := obnote.Root()
	if !ok {
		return nil, nil
	}
	nctx := ctx
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		nctx, cancel = context.WithDeadline(ctx, dl.Add(-recallNoteMargin))
		defer cancel()
	}
	entries, err := obnote.Load(nctx, root)
	if err != nil {
		return nil, err
	}
	seen := hook.LoadInjectedNotes(sessionID)
	exclude := make(map[string]bool)
	for _, e := range entries {
		if seen[display+"/"+e.Rel] {
			exclude[e.Rel] = true
		}
	}
	var out []string
	for _, e := range obnote.Match(db.NewPromptTerms(prompt, parts...), entries, exclude, recallNoteLimit) {
		out = append(out, display+"/"+e.Rel)
	}
	return out, nil
}

var contextLineID = regexp.MustCompile(`(?m)^- #(\d+) `)

// contextIDs 從 context 輸出的索引行取出記憶 id。
func contextIDs(out string) []int64 {
	var ids []int64
	for _, m := range contextLineID.FindAllStringSubmatch(out, -1) {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
