package llm

import (
	"fmt"
	"strconv"
)

// 設定用的環境變數名稱。
const (
	EnvProvider   = "MEMORY_MCP_LLM_PROVIDER"
	EnvAPIKey     = "MEMORY_MCP_LLM_API_KEY"
	EnvBaseURL    = "MEMORY_MCP_LLM_BASE_URL"
	EnvModelCheap = "MEMORY_MCP_LLM_MODEL_CHEAP"
	EnvModelSynth = "MEMORY_MCP_LLM_MODEL_SYNTH"
	EnvDailyCalls = "MEMORY_MCP_LLM_DAILY_CALLS"

	defaultDailyCalls = 50
)

// FromEnv 依環境變數建立 Client；getenv、lookPath 可注入（正式用 os.Getenv、exec.LookPath）。
//
// MEMORY_MCP_LLM_PROVIDER 指定 anthropic／openai／claude-cli 時使用該 provider，缺少必要設定回傳錯誤
// （錯誤只列 env 名稱，不含 key）；off 回傳 nil, nil；未設定時 PATH 上有 claude 就用 claude-cli，
// 否則 nil, nil。會計費的 provider 只有被明確指定才使用：env 裡單純存在 ANTHROPIC_API_KEY／OPENAI_API_KEY 不會觸發。
func FromEnv(getenv func(string) string, lookPath func(string) (string, error)) (Client, error) {
	cheap, synth := getenv(EnvModelCheap), getenv(EnvModelSynth)
	switch p := getenv(EnvProvider); p {
	case "":
		path, err := lookPath("claude")
		if err != nil {
			return nil, nil
		}
		return newClaudeCLI(path, cheap, synth), nil
	case "off":
		return nil, nil
	case "claude-cli":
		path, err := lookPath("claude")
		if err != nil {
			return nil, fmt.Errorf("%s=claude-cli but claude not found on PATH: %w", EnvProvider, err)
		}
		return newClaudeCLI(path, cheap, synth), nil
	case "anthropic":
		key := apiKey(getenv, "ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("%s=anthropic requires %s or ANTHROPIC_API_KEY", EnvProvider, EnvAPIKey)
		}
		return &Anthropic{
			BaseURL:    orDefault(getenv(EnvBaseURL), "https://api.anthropic.com"),
			APIKey:     key,
			CheapModel: orDefault(cheap, "claude-haiku-5-5"),
			SynthModel: orDefault(synth, "claude-sonnet-5-5"),
		}, nil
	case "openai":
		if cheap == "" {
			return nil, fmt.Errorf("%s=openai requires %s", EnvProvider, EnvModelCheap)
		}
		if synth == "" {
			return nil, fmt.Errorf("%s=openai requires %s", EnvProvider, EnvModelSynth)
		}
		base := getenv(EnvBaseURL)
		key := apiKey(getenv, "OPENAI_API_KEY")
		// 自訂 base（例如本機 Ollama）可不帶 key；官方 API 一定要 key。
		if base == "" && key == "" {
			return nil, fmt.Errorf("%s=openai requires %s or OPENAI_API_KEY (or %s for a local server)", EnvProvider, EnvAPIKey, EnvBaseURL)
		}
		return &OpenAI{
			BaseURL:    orDefault(base, "https://api.openai.com/v1"),
			APIKey:     key,
			CheapModel: cheap,
			SynthModel: synth,
		}, nil
	default:
		return nil, fmt.Errorf("unknown %s %q (want anthropic, openai, claude-cli or off)", EnvProvider, p)
	}
}

// DailyLimit 讀 MEMORY_MCP_LLM_DAILY_CALLS：未設定為 50，0 表示不限制，負數或非數字回傳錯誤。
func DailyLimit(getenv func(string) string) (int, error) {
	v := getenv(EnvDailyCalls)
	if v == "" {
		return defaultDailyCalls, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s %q: want a non-negative integer", EnvDailyCalls, v)
	}
	return n, nil
}

// DisabledReason 說明 FromEnv 回傳 nil client 的原因，給 llm status 與 log 用。
func DisabledReason(getenv func(string) string) string {
	if getenv(EnvProvider) == "off" {
		return EnvProvider + "=off"
	}
	return EnvProvider + " unset and claude not found on PATH"
}

func newClaudeCLI(path, cheap, synth string) *ClaudeCLI {
	return &ClaudeCLI{Command: path, CheapModel: orDefault(cheap, "haiku"), SynthModel: orDefault(synth, "sonnet")}
}

// apiKey MEMORY_MCP_LLM_API_KEY 優先，空的才退回該 provider 自己的 key 變數。
func apiKey(getenv func(string) string, fallback string) string {
	if k := getenv(EnvAPIKey); k != "" {
		return k
	}
	return getenv(fallback)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
