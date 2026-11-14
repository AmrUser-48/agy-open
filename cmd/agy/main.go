package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/server"
	"github.com/AmrUser-48/agy-open/internal/scheduler"
	"github.com/AmrUser-48/agy-open/internal/tui"
)

const version = "0.8.4"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "login":
			options, err := parseLoginOptions(os.Args[2:])
			provider := ""
			if err == nil {
				provider, err = selectedProvider(options.providerArgs)
			}
			if err == nil && options.withAPIKey && !auth.IsAPIKeyProvider(provider) {
				err = fmt.Errorf("%s does not use API-key authentication; use gemini-api, ai-studio, gemini-live, openai-api, openai-compatible, openrouter, or groq", provider)
			}
			if err == nil && options.withChatGPTPlan && provider != "openai" {
				err = fmt.Errorf("--with-chatgpt-plan requires the openai OAuth provider")
			}
			if err == nil {
				switch {
				case options.withAPIKey:
					err = auth.NewAPIKeyStore().SetFromReader(provider, os.Stdin)
				case options.withChatGPTPlan:
					err = (&auth.OpenAIManager{}).LoginWithChatGPTPlan(context.Background())
				default:
					err = loginProvider(context.Background(), provider)
				}
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			if options.withAPIKey {
				fmt.Printf("%s API key saved in the separate credential store.\n", providerLabel(provider))
			} else if options.withChatGPTPlan {
				fmt.Println("ChatGPT plan login complete.")
			} else {
				fmt.Printf("%s login complete.\n", providerLabel(provider))
			}
			return
		case "logout":
			provider, err := selectedProvider(os.Args[2:])
			if err == nil {
				err = logoutProvider(provider)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			fmt.Printf("%s credentials removed.\n", providerLabel(provider))
			return
		case "models":
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			modelProvider := cfg.ModelProvider
			if len(os.Args) > 3 {
				fmt.Fprintln(os.Stderr, "agy: usage: agy models [gemini|gemini-api|ai-studio|gemini-live|openai|openai-api|openai-compatible|openrouter|groq]")
				os.Exit(2)
			}
			if len(os.Args) == 3 {
				modelProvider = normalizeModelProviderAlias(os.Args[2])
			}
			if !config.IsSupportedModelProvider(modelProvider) {
				fmt.Fprintf(os.Stderr, "agy: unsupported model provider %q\n", modelProvider)
				os.Exit(2)
			}
			modelCtx, cancelModels := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancelModels()
			options, err := agent.ListProviderModelOptions(modelCtx, cfg, modelProvider)
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			for _, option := range options {
				fmt.Printf("%-36s %s\n", option.ID, option.Label())
			}
			return
		case "agents":
			roles, err := agent.ListRoles()
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			activeRole := cfg.AgentRole
			if activeRole == "" {
				activeRole = "default"
			}
			fmt.Println("Available agent roles:")
			for _, role := range roles {
				marker := " "
				if role.Name == activeRole {
					marker = "*"
				}
				fmt.Printf("%s %-12s %s\n", marker, role.Name, role.Description)
			}
			fmt.Println("\nCustomize ~/.config/agy-open/roles/*.json, or $XDG_CONFIG_HOME/agy-open/roles/*.json.")
			return
		case "task":
			fmt.Fprintln(os.Stderr, "Scheduled tasks are managed in the TUI: run agy, then enter /schedule.")
			os.Exit(2)
		case "serve":
			port := "8080"
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			portSet := false
			for _, arg := range os.Args[2:] {
				switch arg {
				case "--sandbox":
					cfg.EnableTerminalSandbox = true
				case "--no-sandbox":
					cfg.EnableTerminalSandbox = false
				default:
					if strings.HasPrefix(arg, "-") || portSet {
						fmt.Fprintln(os.Stderr, "agy: usage: agy serve [port] [--sandbox|--no-sandbox]")
						os.Exit(2)
					}
					port, portSet = arg, true
				}
			}
			addr := "127.0.0.1:" + port
			root, _ := filepath.Abs(".")
			a, err := agent.New(root, cfg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			defer a.Close()
			taskScheduler, err := openRuntimeScheduler(root, a)
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy scheduler:", err)
				os.Exit(1)
			}
			if err := taskScheduler.Start(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "agy scheduler:", err)
				os.Exit(1)
			}
			defer taskScheduler.Close()
			if err := server.Start(addr, a, taskScheduler); err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			return
		}
	}

	var prompt string
	flag.StringVar(&prompt, "p", "", "run a single prompt and exit")
	flag.StringVar(&prompt, "print", "", "run a single prompt and exit")
	flag.StringVar(&prompt, "prompt", "", "run a single prompt and exit")

	model := flag.String("model", "", "model slug for this run")
	effort := flag.String("effort", "", "reasoning effort: low, medium, or high")
	provider := flag.String("provider", "", "model provider: gemini, gemini-api, ai-studio, gemini-live, openai, openai-api, openai-compatible, openrouter, or groq")
	baseURL := flag.String("base-url", "", "OpenAI-compatible API base URL (usually ending in /v1)")
	agentName := flag.String("agent", "", "agent role for this run")
	var workspace string
	flag.StringVar(&workspace, "workspace", ".", "workspace directory")
	flag.StringVar(&workspace, "cwd", ".", "working directory")
	outputFormat := flag.String("output-format", "text", "output format: text, json, or stream-json")
	inputFormat := flag.String("input-format", "text", "input format: text or stream-json")
	jsonSchema := flag.String("json-schema", "", "JSON schema string or .json file")
	var cont bool
	flag.BoolVar(&cont, "continue", false, "continue the most recent conversation")
	flag.BoolVar(&cont, "c", false, "continue the most recent conversation")
	conversation := flag.String("conversation", "", "resume a conversation by ID")
	danger := flag.Bool("dangerously-skip-permissions", false, "deprecated alias for --autopilot")
	autopilot := flag.Bool("autopilot", false, "enable unrestricted autopilot permissions")
	printTimeout := flag.Duration("print-timeout", 0, "optional response timeout; 0 means unlimited")
	sandbox := flag.Bool("sandbox", false, "enable terminal sandbox mode for this run")
	noSandbox := flag.Bool("no-sandbox", false, "disable terminal sandbox mode for this run")
	login := flag.Bool("login", false, "authenticate with the configured model provider")
	logout := flag.Bool("logout", false, "remove credentials for the configured model provider")
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Println("agy-open", version)
		return
	}
	if *sandbox && *noSandbox {
		fmt.Fprintln(os.Stderr, "agy: --sandbox and --no-sandbox cannot be combined")
		os.Exit(2)
	}
	if *login || *logout {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		provider := cfg.ModelProvider
		if provider == "" {
			provider = "gemini"
		}
		if *login {
			err = loginProvider(context.Background(), provider)
		} else {
			err = logoutProvider(provider)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		if *login {
			fmt.Printf("%s login complete.\n", providerLabel(provider))
		} else {
			fmt.Printf("%s credentials removed.\n", providerLabel(provider))
		}
		return
	}

	if *outputFormat != "text" && *outputFormat != "json" && *outputFormat != "stream-json" {
		fmt.Fprintln(os.Stderr, "agy: invalid --output-format")
		os.Exit(2)
	}
	if *inputFormat != "text" && *inputFormat != "stream-json" {
		fmt.Fprintln(os.Stderr, "agy: invalid --input-format")
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
	if *provider != "" {
		selected := normalizeModelProviderAlias(*provider)
		if err := config.SetSetting(&cfg, "modelProvider", selected); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(2)
		}
	}
	if *baseURL != "" {
		if err := config.SetSetting(&cfg, "modelBaseURL", *baseURL); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(2)
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(*model)), "groq:") {
		if *provider != "" && normalizeModelProviderAlias(*provider) != "groq" {
			fmt.Fprintln(os.Stderr, "agy: a groq:<model-id> requires --provider groq")
			os.Exit(2)
		}
		if *provider == "" && cfg.ModelProvider != "groq" {
			if err := config.SetSetting(&cfg, "modelProvider", "groq"); err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(2)
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(*model)), "nvidia:") {
		if *provider != "" && normalizeModelProviderAlias(*provider) != "nvidia" {
			fmt.Fprintln(os.Stderr, "agy: a nvidia:<model-id> requires --provider nvidia")
			os.Exit(2)
		}
		if *provider == "" && cfg.ModelProvider != "nvidia" {
			if err := config.SetSetting(&cfg, "modelProvider", "nvidia"); err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(2)
			}
		}
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *effort != "" {
		cfg.Effort = *effort
	}
	if *danger || *autopilot {
		cfg.ApprovalMode = "autopilot"
	}
	if *sandbox {
		cfg.EnableTerminalSandbox = true
	}
	if *noSandbox {
		cfg.EnableTerminalSandbox = false
	}

	root, err := filepath.Abs(workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
	a, err := agent.New(root, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
	defer a.Close()

	if strings.TrimSpace(*agentName) != "" {
		if err := a.UseRole(*agentName); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(2)
		}
	}

	if *jsonSchema != "" {
		raw := []byte(*jsonSchema)
		if strings.HasSuffix(*jsonSchema, ".json") {
			var readErr error
			raw, readErr = os.ReadFile(*jsonSchema)
			if readErr != nil {
				fmt.Fprintln(os.Stderr, "agy: read --json-schema:", readErr)
				os.Exit(1)
			}
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			fmt.Fprintln(os.Stderr, "agy: invalid --json-schema:", err)
			os.Exit(2)
		}
		a.SetJSONSchema(schema)
	}
	if conversation != nil && strings.TrimSpace(*conversation) != "" {
		if err := a.ResumeSession(*conversation); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
	} else if cont {
		if err := a.ResumeLast(); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
	}

	if prompt != "" {
		if *inputFormat == "stream-json" {
			fmt.Fprintln(os.Stderr, "agy: stream-json input cannot be combined with -p")
			os.Exit(2)
		}
		start := time.Now()
		ctx, cancel := printContext(*printTimeout)
		defer cancel()
		var buf bytes.Buffer
		runErr := a.RunPrompt(ctx, prompt, &buf)
		runErr = printTimeoutError(runErr, ctx, *printTimeout)
		emitHeadless(*outputFormat, a.SessionID(), buf.String(), runErr, start, a)
		if runErr != nil {
			os.Exit(1)
		}
		return
	}

	if *inputFormat == "stream-json" {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			var event map[string]any
			if json.Unmarshal(sc.Bytes(), &event) != nil {
				continue
			}
			if event["event"] != "user" {
				continue
			}
			msg, _ := event["message"].(map[string]any)
			content, _ := msg["content"].(string)
			if content == "" {
				continue
			}
			start := time.Now()
			var buf bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), *printTimeout)
			runErr := a.RunPrompt(ctx, content, &buf)
			runErr = printTimeoutError(runErr, ctx, *printTimeout)
			cancel()
			emitHeadless("stream-json", a.SessionID(), buf.String(), runErr, start, a)
		}
		if err := sc.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		return
	}

	taskScheduler, err := openRuntimeScheduler(root, a)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy scheduler:", err)
		os.Exit(1)
	}

	if tui.IsTerminal() {
		ui := tui.New(a, taskScheduler)
		// Attach the TUI approval handler before starting timers. A task that
		// became due while AGY was stopped may be queued immediately on Start.
		if err := taskScheduler.Start(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "agy scheduler:", err)
			os.Exit(1)
		}
		defer taskScheduler.Close()
		if err := ui.Run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		return
	}
	if err := taskScheduler.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "agy scheduler:", err)
		os.Exit(1)
	}
	defer taskScheduler.Close()
	a.SetEventSink(func(ev agent.Event) {
		if ev.Kind != "tool_end" || ev.Tool != "shell" || !ev.HasExitCode {
			return
		}
		process := scheduler.ProcessName(ev.Command)
		payload := fmt.Sprintf("process=%s exit_code=%d command=%s", process, ev.ExitCode, ev.Command)
		taskScheduler.NotifyProcessExit(process, ev.ExitCode)
		taskScheduler.PublishEvent("process.exit", payload)
		taskScheduler.PublishEvent("shell.exit", payload)
	})
	if err := a.Repl(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
}

type loginOptions struct {
	providerArgs    []string
	withAPIKey      bool
	withChatGPTPlan bool
}

// parseLoginOptions keeps identity-only login as the default. --browser
// explicitly requests ChatGPT-plan scopes and uses the system browser opened
// by OpenAIManager.LoginWithChatGPTPlan.
func parseLoginOptions(args []string) (loginOptions, error) {
	var options loginOptions
	for _, arg := range args {
		switch arg {
		case "--with-api-key":
			options.withAPIKey = true
		case "--browser", "--with-chatgpt-plan":
			options.withChatGPTPlan = true
		default:
			options.providerArgs = append(options.providerArgs, arg)
		}
	}
	if options.withAPIKey && options.withChatGPTPlan {
		return options, fmt.Errorf("--with-api-key cannot be combined with --browser or --with-chatgpt-plan")
	}
	return options, nil
}

func normalizeModelProviderAlias(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "google", "antigravity", "gemini":
		return "gemini"
	case "gemini-api", "google-gemini-api", "gemini-developer-api":
		return "gemini-api"
	case "ai-studio", "aistudio", "google-ai-studio":
		return "ai-studio"
	case "gemini-live", "live", "google-live":
		return "gemini-live"
	case "openai-api", "platform-openai", "openai-platform":
		return "openai-api"
	case "openrouter", "open-router":
		return "openrouter"
	case "groq", "groq-api":
		return "groq"
	case "nvidia", "nvidia-api", "nvidia-nim", "nvidia-api-catalog":
		return "nvidia"
	case "local", "ollama", "llama.cpp", "llamacpp", "vllm", "lm-studio", "lmstudio", "openai-compatible":
		return "openai-compatible"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func selectedProvider(args []string) (string, error) {
	if len(args) == 0 {
		cfg, err := config.Load()
		if err != nil {
			return "", err
		}
		if cfg.ModelProvider == "" {
			return "gemini", nil
		}
		if !config.IsSupportedModelProvider(cfg.ModelProvider) {
			return "", fmt.Errorf("unsupported configured model provider %q", cfg.ModelProvider)
		}
		return cfg.ModelProvider, nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("usage: agy login [provider] [--browser|--with-chatgpt-plan|--with-api-key] or agy logout [provider]")
	}
	provider := normalizeModelProviderAlias(args[0])
	if !config.IsSupportedModelProvider(provider) {
		return "", fmt.Errorf("unknown provider %q; use gemini, gemini-api, ai-studio, gemini-live, openai, openai-api, openai-compatible, openrouter, groq, or nvidia", args[0])
	}
	return provider, nil
}

func loginProvider(ctx context.Context, provider string) error {
	switch provider {
	case "gemini":
		return (&auth.Manager{}).Login(ctx)
	case "openai":
		return (&auth.OpenAIManager{}).Login(ctx)
	default:
		if auth.IsAPIKeyProvider(provider) {
			return fmt.Errorf("%s uses an API key; provide it on stdin with 'agy login %s --with-api-key'", providerLabel(provider), provider)
		}
		return fmt.Errorf("unsupported model provider %q", provider)
	}
}

func logoutProvider(provider string) error {
	switch provider {
	case "gemini":
		return (&auth.Manager{}).Logout()
	case "openai":
		return (&auth.OpenAIManager{}).Logout()
	default:
		if auth.IsAPIKeyProvider(provider) {
			return auth.NewAPIKeyStore().Remove(provider)
		}
		return fmt.Errorf("unsupported model provider %q", provider)
	}
}

func providerLabel(provider string) string {
	switch provider {
	case "gemini":
		return "Google Antigravity"
	case "gemini-api":
		return "Google Gemini Developer API"
	case "ai-studio":
		return "Google AI Studio"
	case "gemini-live":
		return "Google Gemini Live"
	case "openai":
		return "ChatGPT OAuth"
	case "openai-api":
		return "OpenAI API"
	case "openai-compatible":
		return "OpenAI-compatible server"
	case "openrouter":
		return "OpenRouter"
	case "groq":
		return "Groq API"
	case "nvidia":
		return "NVIDIA API Catalog"
	default:
		return provider
	}
}

func emitHeadless(format, id, response string, err error, start time.Time, a *agent.Agent) {
	status := "SUCCESS"
	if err != nil {
		status = "ERROR"
	}
	if format == "text" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
		}
		fmt.Print(response)
		if response != "" && !strings.HasSuffix(response, "\n") {
			fmt.Println()
		}
		return
	}

	envelope := map[string]any{
		"conversation_id":  id,
		"status":           status,
		"response":         strings.TrimSuffix(response, "\n"),
		"duration_seconds": time.Since(start).Seconds(),
		"num_turns":        1,
	}
	if err != nil {
		envelope["error"] = err.Error()
	}
	b, _ := json.Marshal(envelope)
	if format == "json" {
		fmt.Println(string(b))
		return
	}

	initEvent := map[string]any{
		"event":             "init",
		"conversation_id":  id,
		"model":             a.Model(),
		"permission_mode":  a.ApprovalMode(),
	}
	ib, _ := json.Marshal(initEvent)
	fmt.Println(string(ib))

	step := map[string]any{
		"event":            "step_update",
		"conversation_id": id,
		"step_index":       0,
		"state":            "DONE",
		"step_type":        "agent_response",
		"text_delta":       strings.TrimSuffix(response, "\n"),
	}
	sb, _ := json.Marshal(step)
	fmt.Println(string(sb))
	fmt.Println(string(b))
}


func printContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithCancel(context.Background())
}

func printTimeoutError(err error, ctx context.Context, timeout time.Duration) error {
	if timeout > 0 && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("agent stopped after --print-timeout=%s; workspace changes already made were retained", timeout)
	}
	return err
}
