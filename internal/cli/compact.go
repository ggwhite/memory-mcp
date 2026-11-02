package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ggwhite/memory-mcp/internal/compact"
	"github.com/ggwhite/memory-mcp/internal/summarize"

	"github.com/spf13/cobra"
)

// compactRunner 產生月度彙整用的 LLM runner，測試時替換成假實作。
var compactRunner summarize.Runner = summarize.ClaudeRunner

func init() {
	compactCmd.Flags().String("older-than", "60d", "only compact summaries created before this age (Nd format)")
	compactCmd.Flags().String("project", "", "limit to one project")
	compactCmd.Flags().Bool("apply", false, "actually merge and delete (default is a dry run)")
}

var compactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Roll up old summaries into one summary per project and month (dry run unless --apply)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
			return fmt.Errorf("compact only supports local DB, not --remote")
		}
		olderThan, _ := cmd.Flags().GetString("older-than")
		project, _ := cmd.Flags().GetString("project")
		apply, _ := cmd.Flags().GetBool("apply")

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		groups, err := compact.Plan(d, olderThan, project)
		if err != nil {
			return err
		}
		printCompactPlan(groups)
		if !apply {
			fmt.Println("Dry run: nothing changed, re-run with --apply to merge")
			return nil
		}
		if len(groups) == 0 {
			return nil
		}

		// 任何變更之前先備份所有即將被刪除的記憶。
		backupDir := filepath.Join(filepath.Dir(resolveDBPath()), "backups")
		backup, err := compact.WriteBackup(backupDir, groups, time.Now())
		if err != nil {
			return err
		}
		fmt.Printf("Backup: %s\n", backup)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		outcomes := compact.Apply(ctx, d, groups, compact.Deps{
			Run:  compactRunner,
			Logf: func(format string, args ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) },
		})
		var merged, deleted, skipped int
		for _, o := range outcomes {
			if o.Err != nil {
				skipped++
				continue
			}
			merged++
			deleted += len(o.Group.Memories)
			fmt.Printf("%s %s: %s -> #%d\n", o.Group.Project, o.Group.Month, idRefs(o.Group.IDs()), o.NewID)
		}
		fmt.Printf("Merged %d groups (%d memories deleted), %d skipped\n", merged, deleted, skipped)
		if merged == 0 {
			cmd.SilenceUsage = true
			return fmt.Errorf("compact: all %d groups skipped", skipped)
		}
		return nil
	},
}

// printCompactPlan 列出每個要合併的群組與合計，dry run 與 --apply 都會先印。
func printCompactPlan(groups []compact.Group) {
	var rows, chars int
	for _, g := range groups {
		rows += len(g.Memories)
		chars += g.Chars()
		fmt.Printf("%s %s: %d memories, %d chars, %s\n", g.Project, g.Month, len(g.Memories), g.Chars(), idRefs(g.IDs()))
	}
	fmt.Printf("Total: %d groups, %d memories, %d chars\n", len(groups), rows, chars)
}

func idRefs(ids []int64) string {
	refs := make([]string, len(ids))
	for i, id := range ids {
		refs[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(refs, " ")
}
