package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/llm"
	"github.com/ggwhite/memory-mcp/internal/mental"

	"github.com/spf13/cobra"
)

// mentalLLM 建立 mental build 用的 LLM client，測試時替換成假實作；回傳 nil 表示沒有設定 LLM。
var mentalLLM = resolveLLM

func init() {
	mentalAddCmd.Flags().String("project", "", "project the question belongs to (required)")
	mentalListCmd.Flags().String("project", "", "limit to one project")
	mentalBuildCmd.Flags().String("project", "", "build one project's mental models")
	mentalBuildCmd.Flags().Bool("all", false, "build every project's mental models")
	mentalBuildCmd.Flags().Bool("force", false, "rebuild even when the project has no new memories")
	mentalCmd.AddCommand(mentalAddCmd, mentalListCmd, mentalRmCmd, mentalBuildCmd)
	rootCmd.AddCommand(mentalCmd)
}

var mentalCmd = &cobra.Command{
	Use:   "mental",
	Short: "Standing per-project questions whose LLM-built answers are shown in context",
}

var mentalAddCmd = &cobra.Command{
	Use:   "add --project p <question>",
	Short: "Add a standing question for a project (answer is filled by mental build)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("mental add"); err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			return fmt.Errorf("mental add: --project is required")
		}
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		id, err := d.AddMentalModel(project, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added m%d (run `memory-mcp mental build --project %s` to answer it)\n", id, project)
		return nil
	},
}

var mentalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List mental models and their current answers",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("mental list"); err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		ms, err := d.ListMentalModels(db.MentalModelFilter{Project: project, AllProjects: project == ""})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(ms)
		}
		out := cmd.OutOrStdout()
		for _, m := range ms {
			if m.Answer == "" {
				fmt.Fprintf(out, "m%d %s (not built) Q: %s\n", m.ID, m.Project, m.Question)
				continue
			}
			fmt.Fprintf(out, "m%d %s %s %s Q: %s\n", m.ID, m.Project, m.Updated.Format("2006-01-02"), m.Source, m.Question)
			for _, l := range strings.Split(m.Answer, "\n") {
				fmt.Fprintf(out, "  %s\n", l)
			}
		}
		return nil
	},
}

var mentalRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Delete a mental model (accepts 3 or m3)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("mental rm"); err != nil {
			return err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "m"), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid mental model id %q", args[0])
		}
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.DeleteMentalModel(id); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed m%d\n", id)
		return nil
	},
}

var mentalBuildCmd = &cobra.Command{
	Use:   "build (--project p | --all)",
	Short: "Answer mental models with the LLM (skips projects without new memories unless --force)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := refuseRemote("mental build"); err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		all, _ := cmd.Flags().GetBool("all")
		force, _ := cmd.Flags().GetBool("force")
		if all == (project != "") {
			return fmt.Errorf("mental build: specify exactly one of --project or --all")
		}
		out := cmd.OutOrStdout()

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		client, err := mentalLLM(d)
		if err != nil {
			return err
		}
		if client == nil {
			fmt.Fprintf(out, "LLM not configured (%s), nothing built\n", llm.DisabledReason(os.Getenv))
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := mental.Build(ctx, d, client, mental.Options{Project: project, All: all, Force: force})
		if err != nil {
			return err
		}
		printMentalBuild(out, res)
		if len(res.Built) == 0 && len(res.Failures) > 0 {
			cmd.SilenceUsage = true
			return fmt.Errorf("mental build: all %d attempted builds failed", len(res.Failures))
		}
		return nil
	},
}

// printMentalBuild 逐筆列出 build 結果與合計。
func printMentalBuild(out io.Writer, res mental.Result) {
	for _, m := range res.Built {
		fmt.Fprintf(out, "built m%d %s: %s (%d sources)\n", m.ID, m.Project, db.Snippet(m.Question, 80), len(m.SourceIDs))
	}
	for _, m := range res.Unchanged {
		fmt.Fprintf(out, "unchanged m%d %s: no new memories since last build\n", m.ID, m.Project)
	}
	for _, m := range res.Empty {
		fmt.Fprintf(out, "empty m%d %s: no memories to build from\n", m.ID, m.Project)
	}
	for _, f := range res.Failures {
		fmt.Fprintf(out, "skipped m%d: %v\n", f.Model.ID, f.Err)
	}
	fmt.Fprintf(out, "Total: %d built, %d unchanged, %d empty, %d failed\n",
		len(res.Built), len(res.Unchanged), len(res.Empty), len(res.Failures))
}

// buildMentalAfterSummary SessionEnd worker 存好 summary 後，對該 project 做增量 mental build；
// 只在背景 worker 內呼叫，沒有本機 DB（--remote）、沒有 LLM 或 project 沒有 mental model 時直接返回，
// 結果與失敗只寫進 summarize log。
func buildMentalAfterSummary(d *db.DB, client llm.Client, project, sessionID string) {
	if d == nil || client == nil {
		return
	}
	logf := func(format string, args ...any) {
		summarizeLogf(sessionID+" "+project+" "+format, args...)
	}
	ms, err := d.ListMentalModels(db.MentalModelFilter{Project: project})
	if err != nil {
		logf("mental: %v", err)
		return
	}
	if len(ms) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := mental.Build(ctx, d, client, mental.Options{Project: project})
	if err != nil {
		logf("mental: %v", err)
		return
	}
	for _, m := range res.Built {
		logf("mental built m%d", m.ID)
	}
	for _, f := range res.Failures {
		logf("mental skipped m%d: %v", f.Model.ID, f.Err)
	}
}
