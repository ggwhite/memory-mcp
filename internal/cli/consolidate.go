package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ggwhite/memory-mcp/internal/compact"
	"github.com/ggwhite/memory-mcp/internal/consolidate"
	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/llm"

	"github.com/spf13/cobra"
)

// consolidateLLM 建立整併用的 LLM client，測試時替換成假實作；回傳 nil 表示走啟發式。
var consolidateLLM = resolveLLM

func init() {
	consolidateCmd.Flags().String("project", "", "consolidate one project")
	consolidateCmd.Flags().Bool("all", false, "consolidate every project")
	consolidateCmd.Flags().String("type", consolidate.DefaultType, "memory type to consolidate")
	consolidateCmd.Flags().Float64("threshold", consolidate.DefaultTrigramThreshold, "character-trigram Jaccard similarity threshold (used when a pair lacks comparable embeddings)")
	consolidateCmd.Flags().Float64("cosine-threshold", consolidate.DefaultCosineThreshold, "embedding cosine similarity threshold (used when both memories have a vector from the same model)")
	consolidateCmd.Flags().Bool("apply", false, "write observations (default only prints the plan)")

	observationListCmd.Flags().String("project", "", "limit to one project")
	observationCmd.AddCommand(observationListCmd, observationDeleteCmd)
	rootCmd.AddCommand(consolidateCmd, observationCmd)
}

// refuseRemote 需要本機 DB 的指令在 --remote / MEMORY_MCP_REMOTE 下一律拒絕。
func refuseRemote(name string) error {
	if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
		return fmt.Errorf("%s only supports local DB, not --remote", name)
	}
	return nil
}

var consolidateCmd = &cobra.Command{
	Use:   "consolidate",
	Short: "Merge near-duplicate memories into observations (plan only unless --apply)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("consolidate"); err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		all, _ := cmd.Flags().GetBool("all")
		typ, _ := cmd.Flags().GetString("type")
		trigram, _ := cmd.Flags().GetFloat64("threshold")
		cosine, _ := cmd.Flags().GetFloat64("cosine-threshold")
		apply, _ := cmd.Flags().GetBool("apply")
		if all == (project != "") {
			return fmt.Errorf("consolidate: specify exactly one of --project or --all")
		}
		out := cmd.OutOrStdout()

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		client, err := consolidateLLM(d)
		if err != nil {
			return err
		}
		if client == nil {
			fmt.Fprintf(out, "LLM: not configured (%s), using heuristic\n", llm.DisabledReason(os.Getenv))
		} else {
			fmt.Fprintf(out, "LLM: %s\n", client.Name())
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := consolidate.Propose(ctx, d, consolidate.Options{
			Project: project, All: all, Type: typ, LLM: client,
			Thresholds: consolidate.Thresholds{Trigram: trigram, Cosine: cosine},
		})
		if err != nil {
			return err
		}
		printConsolidatePlan(out, res)
		if len(res.Proposals) == 0 && len(res.Failures) > 0 {
			cmd.SilenceUsage = true
			return fmt.Errorf("consolidate: all %d clusters failed", len(res.Failures))
		}
		if !apply {
			fmt.Fprintln(out, "Plan only: nothing changed, re-run with --apply to write")
			return nil
		}
		if len(res.Proposals) == 0 {
			return nil
		}

		// 任何變更之前先備份既有 observations（含已停用）與本次提案，與 compact 同目錄、同命名規則。
		before, err := d.ListObservations(db.ObservationFilter{AllProjects: true, IncludeInactive: true})
		if err != nil {
			return err
		}
		backupDir := filepath.Join(filepath.Dir(resolveDBPath()), "backups")
		backup, err := compact.WriteJSONBackup(backupDir, "consolidate-"+time.Now().Format("20060102-150405"), map[string]any{
			"observations": before,
			"proposals":    res.Proposals,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Backup: %s\n", backup)

		ids, err := consolidate.Apply(d, res.Proposals)
		if err != nil {
			return err
		}
		for i, p := range res.Proposals {
			p.ID = ids[i]
			fmt.Fprintf(out, "%s %s: %s\n", p.Project, p.Type, strings.TrimPrefix(db.ObservationLine(p, 120), "- "))
		}
		fmt.Fprintf(out, "Wrote %d observations\n", len(ids))
		return nil
	},
}

// printConsolidatePlan 列出提案（新增或更新既有 o<id>）、LLM 失敗而跳過的群組與合計；plan 與 --apply 都會先印。
func printConsolidatePlan(out io.Writer, res consolidate.Result) {
	var created, refined int
	for _, p := range res.Proposals {
		label := "new"
		if p.ID != 0 {
			label = fmt.Sprintf("o%d refine", p.ID)
			refined++
		} else {
			created++
		}
		fmt.Fprintf(out, "%s %s: %s ×%d %s %s (ev: %s)\n", p.Project, p.Type, label, p.ProofCount, p.Source, db.Snippet(p.Content, 120), idRefs(p.Evidence))
	}
	for _, f := range res.Failures {
		fmt.Fprintf(out, "%s %s: skipped %s: %v\n", f.Cluster.Project, f.Cluster.Type, idRefs(f.Cluster.IDs()), f.Err)
	}
	fmt.Fprintf(out, "Total: %d proposed (%d new, %d refine), %d unchanged, %d rejected, %d failed\n",
		len(res.Proposals), created, refined, res.Unchanged, res.Rejected, len(res.Failures))
}

var observationCmd = &cobra.Command{
	Use:   "observation",
	Short: "List or delete observations produced by consolidate",
}

var observationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active observations",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("observation list"); err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		obs, err := d.ListObservations(db.ObservationFilter{Project: project, AllProjects: project == ""})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(obs)
		}
		out := cmd.OutOrStdout()
		for _, o := range obs {
			fmt.Fprintf(out, "o%d %s %s ×%d %s %s %s (ev: %s)\n", o.ID, o.Project, o.Type, o.ProofCount, o.Source,
				o.Updated.Format("2006-01-02"), db.Snippet(o.Content, 120), idRefs(o.Evidence))
		}
		return nil
	},
}

var observationDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Deactivate an observation (accepts 3 or o3); its evidence memories show in context again",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("observation delete"); err != nil {
			return err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "o"), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid observation id %q", args[0])
		}
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.DeactivateObservation(id); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Deactivated o%d (evidence memories show in context again)\n", id)
		return nil
	},
}
