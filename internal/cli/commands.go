package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"memory-mcp/internal/db"
	"memory-mcp/internal/embed"
	"memory-mcp/internal/hook"
	"memory-mcp/internal/httpapi"
	memcp "memory-mcp/internal/mcp"
	"memory-mcp/internal/summarize"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

var (
	dbPath     string
	jsonFlag   bool
	remoteFlag string
)

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "memory-mcp", "memory.db")
}

func openDB() (*db.DB, error) {
	path := dbPath
	if path == "" {
		path = os.Getenv("MEMORY_MCP_DB")
	}
	if path == "" {
		path = defaultDBPath()
	}
	d, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	d.SetEmbedder(embed.NewOllamaEmbedderFromEnv())
	return d, nil
}

// openStore 依 --remote / MEMORY_MCP_REMOTE 決定要操作本機 SQLite
// 還是透過 HTTP 轉發到遠端 memory-mcp（例如 SSH tunnel 過去的另一台機器）。
func openStore() (db.Store, error) {
	remote := remoteFlag
	if remote == "" {
		remote = os.Getenv("MEMORY_MCP_REMOTE")
	}
	if remote != "" {
		return httpapi.NewClient(remote), nil
	}
	return openDB()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

var rootCmd = &cobra.Command{
	Use:   "memory-mcp",
	Short: "Cross-session persistent memory for AI coding agents",
}

// Execute 執行 CLI root command。
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database path (default ~/.local/share/memory-mcp/memory.db)")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "output JSON format")
	rootCmd.PersistentFlags().StringVar(&remoteFlag, "remote", "", "remote memory-mcp REST API URL (e.g. http://127.0.0.1:8766); overrides --db. Also settable via MEMORY_MCP_REMOTE env var")

	storeCmd.Flags().StringP("type", "t", "", "memory type (feedback|til|summary|knowledge)")
	storeCmd.MarkFlagRequired("type")
	storeCmd.Flags().String("tags", "", "comma-separated tags")
	storeCmd.Flags().String("project", "", "project name")

	searchCmd.Flags().String("type", "", "filter by memory type")
	searchCmd.Flags().IntP("limit", "n", 5, "max results")
	searchCmd.Flags().Bool("compact", false, "print one index line per result")

	listCmd.Flags().String("type", "", "filter by memory type")
	listCmd.Flags().IntP("limit", "n", 10, "max results")
	listCmd.Flags().String("since", "", "show memories since (Nd format, e.g. 7d)")

	exportCmd.Flags().String("format", "json", "export format")

	contextCmd.Flags().String("type", "", "filter by memory type")
	contextCmd.Flags().String("project", "", "filter to a specific project")
	contextCmd.Flags().IntP("limit", "n", 20, "max memories to include")
	contextCmd.Flags().Bool("full", false, "print full content instead of index lines")
	contextCmd.Flags().Bool("hook", false, "Claude Code SessionStart hook mode: read hook JSON from stdin, use cwd basename as project, never fail")

	timelineCmd.Flags().Int("before", 3, "earlier memories to show (max 10)")
	timelineCmd.Flags().Int("after", 3, "later memories to show (max 10)")

	serveCmd.Flags().String("http", "", "listen on this addr as an HTTP MCP server (e.g. 127.0.0.1:8766); empty = stdio")
	serveCmd.Flags().String("http-api", "", "listen on this addr as a REST JSON API server (always local DB, for --remote clients to connect to)")

	summarizeCmd.Flags().Bool("hook", false, "Claude Code SessionEnd hook mode: spawn a background worker and return immediately")
	summarizeCmd.Flags().String("worker", "", "internal: run summary from saved hook input file")
	summarizeCmd.Flags().MarkHidden("worker")

	rootCmd.AddCommand(storeCmd, searchCmd, listCmd, deleteCmd, updateCmd, statsCmd, exportCmd, importCmd, serveCmd, contextCmd, reindexCmd, getCmd, timelineCmd, summarizeCmd)
}

var storeCmd = &cobra.Command{
	Use:   "store <content>",
	Short: "Store a new memory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		tags, _ := cmd.Flags().GetString("tags")
		project, _ := cmd.Flags().GetString("project")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		mem := &db.Memory{Type: typ, Content: args[0], Tags: tags, Project: project}
		id, err := d.Store(mem)
		if err != nil {
			return err
		}
		if mem.Redacted > 0 {
			fmt.Fprintf(os.Stderr, "已遮蔽 %d 處敏感內容\n", mem.Redacted)
		}
		if jsonFlag {
			return printJSON(map[string]any{"id": id, "redacted": mem.Redacted})
		}
		fmt.Printf("Stored memory #%d\n", id)
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search memories with FTS5",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		results, err := d.Search(db.SearchOptions{
			Query: args[0], Type: typ, Limit: limit,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(results)
		}
		if compact, _ := cmd.Flags().GetBool("compact"); compact {
			for _, r := range results {
				fmt.Println(db.CompactLine(r.Memory, 80, true))
			}
			return nil
		}
		for i, r := range results {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(db.FormatFull(r.Memory))
		}
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List memories",
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")
		since, _ := cmd.Flags().GetString("since")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.List(db.ListOptions{
			Type: typ, Limit: limit, Since: since,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		for i, m := range memories {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(db.FormatFull(m))
		}
		return nil
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a memory by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		if err := d.Delete(id); err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"deleted": true})
		}
		fmt.Printf("Deleted memory #%d\n", id)
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update <id> <content>",
	Short: "Update a memory's content",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		if err := d.Update(id, args[1]); err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"updated": true})
		}
		fmt.Printf("Updated memory #%d\n", id)
		return nil
	},
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show memory statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		s, err := d.Stats()
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(s)
		}
		fmt.Printf("Total: %d\n", s.Total)
		for typ, n := range s.ByType {
			fmt.Printf("  %s: %d\n", typ, n)
		}
		if s.Earliest != "" {
			fmt.Printf("Earliest: %s\n", strings.Split(s.Earliest, "T")[0])
			fmt.Printf("Latest: %s\n", strings.Split(s.Latest, "T")[0])
		}
		return nil
	},
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all memories as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.ExportAll()
		if err != nil {
			return err
		}
		return printJSON(memories)
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server (stdio by default, or HTTP with --http)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// --http-api 是給其他機器用 --remote 連過來的中央伺服器模式，
		// 一定操作本機 DB，不會再往外轉發。
		if apiAddr, _ := cmd.Flags().GetString("http-api"); apiAddr != "" {
			d, err := openDB()
			if err != nil {
				return err
			}
			defer d.Close()
			return http.ListenAndServe(apiAddr, httpapi.NewServer(d).Handler())
		}

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		srv := memcp.NewServer(d).MCPServer()

		// --http 啟 StreamableHTTP 單一 server，各 session 連同一個 URL
		// （對齊 docs-rag），避免每個 session fork 一支 stdio server。
		if addr, _ := cmd.Flags().GetString("http"); addr != "" {
			return mcpserver.NewStreamableHTTPServer(srv).Start(addr)
		}
		return mcpserver.ServeStdio(srv)
	},
}

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show a bounded memory digest for context injection",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isHook, _ := cmd.Flags().GetBool("hook"); isHook {
			runContextHook()
			return nil
		}
		typ, _ := cmd.Flags().GetString("type")
		project, _ := cmd.Flags().GetString("project")
		limit, _ := cmd.Flags().GetInt("limit")
		full, _ := cmd.Flags().GetBool("full")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		summary, err := d.Context(db.ContextOptions{
			Type: typ, Project: project, Limit: limit, Full: full,
		})
		if err != nil {
			return err
		}
		fmt.Print(summary)
		return nil
	},
}

// runContextHook SessionStart hook：任何錯誤都靜默，不輸出也不回傳錯誤。
func runContextHook() {
	if os.Getenv(hook.SummarizingEnv) == "1" {
		return
	}
	in := hook.ReadStdin()
	d, err := openStore()
	if err != nil {
		return
	}
	defer d.Close()
	out, err := d.Context(db.ContextOptions{Project: hook.Project(in.Cwd)})
	if err != nil {
		return
	}
	fmt.Print(out)
}

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Auto-summarize a Claude Code session into a summary memory (SessionEnd hook)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if file, _ := cmd.Flags().GetString("worker"); file != "" {
			runSummarizeWorker(file)
			return nil
		}
		if isHook, _ := cmd.Flags().GetBool("hook"); isHook {
			runSummarizeHook()
			return nil
		}
		return fmt.Errorf("use --hook")
	},
}

// summarizeLogf 追加一行到 DB 同目錄的 summarize.log。
func summarizeLogf(format string, args ...any) {
	dir := filepath.Dir(defaultDBPath())
	if dbPath != "" {
		dir = filepath.Dir(dbPath)
	}
	f, err := os.OpenFile(filepath.Join(dir, "summarize.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// runSummarizeHook 前景：把 hook JSON 存成暫存檔，fork 背景 worker 後立即返回。
func runSummarizeHook() {
	if os.Getenv(hook.SummarizingEnv) == "1" {
		return
	}
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		summarizeLogf("- - hook: stdin is a terminal")
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		summarizeLogf("- - hook: empty stdin")
		return
	}
	tmp, err := os.CreateTemp("", "memory-mcp-summarize-*.json")
	if err != nil {
		summarizeLogf("- - hook: temp file: %v", err)
		return
	}
	tmp.Write(data)
	tmp.Close()

	exe, err := os.Executable()
	if err != nil {
		summarizeLogf("- - hook: executable: %v", err)
		os.Remove(tmp.Name())
		return
	}
	workerArgs := []string{"summarize", "--worker", tmp.Name()}
	if dbPath != "" {
		workerArgs = append(workerArgs, "--db", dbPath)
	}
	if remoteFlag != "" {
		workerArgs = append(workerArgs, "--remote", remoteFlag)
	}
	c := exec.Command(exe, workerArgs...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		summarizeLogf("- - hook: start worker: %v", err)
		os.Remove(tmp.Name())
		return
	}
	c.Process.Release()
}

// runSummarizeWorker 背景：讀暫存檔後刪除，執行摘要並寫 log。
func runSummarizeWorker(file string) {
	data, err := os.ReadFile(file)
	os.Remove(file)
	if err != nil {
		summarizeLogf("- - worker: read input: %v", err)
		return
	}
	in := hook.Read(strings.NewReader(string(data)))

	d, err := openStore()
	if err != nil {
		summarizeLogf("%s - worker: open store: %v", in.SessionID, err)
		return
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	summarize.Run(ctx, in, summarize.Deps{
		Store: d,
		Run:   summarize.ClaudeRunner,
		Logf:  summarizeLogf,
	})
}

var getCmd = &cobra.Command{
	Use:   "get <id>...",
	Short: "Show full content of memories by id",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := db.ParseIDs(strings.Join(args, " "))
		if err != nil {
			return err
		}
		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.GetMany(ids)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		found := make(map[int64]bool, len(memories))
		for i, m := range memories {
			found[m.ID] = true
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(db.FormatFull(m))
		}
		for _, id := range ids {
			if !found[id] {
				fmt.Fprintf(os.Stderr, "#%d not found\n", id)
			}
		}
		return nil
	},
}

var timelineCmd = &cobra.Command{
	Use:   "timeline <id>",
	Short: "Show memories before and after a memory in the same project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}
		before, _ := cmd.Flags().GetInt("before")
		after, _ := cmd.Flags().GetInt("after")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.Timeline(id, before, after)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		for _, line := range db.TimelineLines(memories, id, 80) {
			fmt.Println(line)
		}
		return nil
	},
}

var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Backfill or refresh semantic search embeddings for stored memories",
	RunE: func(cmd *cobra.Command, args []string) error {
		if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
			return fmt.Errorf("reindex only operates on the local database, not --remote")
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		stats, err := d.Reindex(context.Background())
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(stats)
		}
		fmt.Printf("Reindexed %d/%d memories (%d failed)\n", stats.Processed, stats.Total, stats.Failed)
		return nil
	},
}

var importCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import memories from JSON file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}

		var memories []db.Memory
		if err := json.Unmarshal(data, &memories); err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		n, err := d.ImportBatch(memories)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"imported": n})
		}
		fmt.Printf("Imported %d memories\n", n)
		return nil
	},
}
