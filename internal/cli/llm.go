package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/llm"

	"github.com/spf13/cobra"
)

// llmLookPath 尋找 claude 執行檔，測試時替換。
var llmLookPath = exec.LookPath

// redactForLLM 送給網路 provider 前用 db.Sanitize 遮蔽金鑰與 <private> 區段。
func redactForLLM(s string) string {
	out, _ := db.Sanitize(s)
	return out
}

// resolveLLM 依環境變數建立 LLM client，掛上遮蔽與每日次數上限；d 為 nil（例如 --remote）時不計次數。
// 沒有設定 LLM 時回傳 nil, nil。
func resolveLLM(d *db.DB) (llm.Client, error) {
	c, err := llm.FromEnv(os.Getenv, llmLookPath)
	if err != nil || c == nil {
		return nil, err
	}
	llm.SetRedact(c, redactForLLM)
	limit, err := llm.DailyLimit(os.Getenv)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return c, nil
	}
	return llm.WithBudget(c, d.LLMUsage(), limit), nil
}

func init() {
	llmCmd.AddCommand(llmStatusCmd)
	rootCmd.AddCommand(llmCmd)
}

var llmCmd = &cobra.Command{
	Use:   "llm",
	Short: "Inspect the optional LLM provider configuration",
}

var llmStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the resolved LLM provider, models and today's call budget (never prints the API key)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if remoteFlag != "" || os.Getenv("MEMORY_MCP_REMOTE") != "" {
			return fmt.Errorf("llm status only supports local DB, not --remote")
		}
		out := cmd.OutOrStdout()
		c, err := llm.FromEnv(os.Getenv, llmLookPath)
		if err != nil {
			fmt.Fprintf(out, "disabled: %v\n", err)
			return nil
		}
		if c == nil {
			fmt.Fprintf(out, "disabled: %s\n", llm.DisabledReason(os.Getenv))
			return nil
		}
		fmt.Fprintf(out, "provider: %s\n", c.Name())
		fmt.Fprintf(out, "cheap: %s\n", llm.ModelFor(c, llm.TierCheap))
		fmt.Fprintf(out, "synth: %s\n", llm.ModelFor(c, llm.TierSynth))

		limit, err := llm.DailyLimit(os.Getenv)
		if err != nil {
			return err
		}
		if limit == 0 {
			fmt.Fprintln(out, "budget: unlimited")
			return nil
		}
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		used, err := d.LLMUsage().Calls(time.Now().Format("2006-01-02"))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "budget: %d/%d calls today\n", used, limit)
		return nil
	},
}
