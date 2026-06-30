package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"memory-mcp/internal/db"
	memcp "memory-mcp/internal/mcp"

	"github.com/spf13/cobra"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var (
	dbPath   string
	jsonFlag bool
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
	return db.Open(path)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func formatMemory(m db.Memory) string {
	line := fmt.Sprintf("#%d [%s] %s", m.ID, m.Type, m.Created.Format("2006-01-02"))
	if m.Tags != "" {
		line += fmt.Sprintf("  tags:%s", m.Tags)
	}
	if m.Project != "" {
		line += fmt.Sprintf("  project:%s", m.Project)
	}
	line += "\n  " + m.Content
	return line
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

	storeCmd.Flags().StringP("type", "t", "", "memory type (feedback|til|summary|knowledge)")
	storeCmd.MarkFlagRequired("type")
	storeCmd.Flags().String("tags", "", "comma-separated tags")
	storeCmd.Flags().String("project", "", "project name")

	searchCmd.Flags().String("type", "", "filter by memory type")
	searchCmd.Flags().IntP("limit", "n", 5, "max results")

	listCmd.Flags().String("type", "", "filter by memory type")
	listCmd.Flags().IntP("limit", "n", 10, "max results")
	listCmd.Flags().String("since", "", "show memories since (Nd format, e.g. 7d)")

	exportCmd.Flags().String("format", "json", "export format")

	rootCmd.AddCommand(storeCmd, searchCmd, listCmd, deleteCmd, updateCmd, statsCmd, exportCmd, importCmd, serveCmd)
}

var storeCmd = &cobra.Command{
	Use:   "store <content>",
	Short: "Store a new memory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		tags, _ := cmd.Flags().GetString("tags")
		project, _ := cmd.Flags().GetString("project")

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		id, err := d.Store(&db.Memory{
			Type: typ, Content: args[0], Tags: tags, Project: project,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"id": id})
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

		d, err := openDB()
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
		for i, r := range results {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(formatMemory(r.Memory))
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

		d, err := openDB()
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
			fmt.Println(formatMemory(m))
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

		d, err := openDB()
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

		d, err := openDB()
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
		d, err := openDB()
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
		d, err := openDB()
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
	Short: "Start stdio MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		srv := memcp.NewServer(d).MCPServer()
		return mcpserver.ServeStdio(srv)
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

		d, err := openDB()
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
