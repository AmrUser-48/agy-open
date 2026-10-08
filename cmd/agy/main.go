package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/tui"
)

const version = "0.5.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "login":
			if err := (&auth.Manager{}).Login(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			fmt.Println("Google login complete.")
			return
		case "logout":
			if err := (&auth.Manager{}).Logout(); err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			fmt.Println("Google credentials removed.")
			return
		case "models":
			cfg, _ := config.Load()
			client, _ := gemini.New(cfg.Model, &auth.Manager{})
			models, err := client.ListModels(context.Background())
			if err != nil {
				fmt.Fprintln(os.Stderr, "agy:", err)
				os.Exit(1)
			}
			for _, model := range models {
				fmt.Println(model)
			}
			return
		case "agents":
			fmt.Println("default")
			return
		}
	}

	var prompt string
	flag.StringVar(&prompt, "p", "", "run a single prompt and exit")
	flag.StringVar(&prompt, "print", "", "run a single prompt and exit")
	flag.StringVar(&prompt, "prompt", "", "run a single prompt and exit")

	model := flag.String("model", "", "model slug for this run")
	effort := flag.String("effort", "", "reasoning effort: low, medium, or high")
	agentName := flag.String("agent", "", "agent for this run")
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
	danger := flag.Bool("dangerously-skip-permissions", false, "auto-approve all tool permission requests")
	printTimeout := flag.Duration("print-timeout", 5*time.Minute, "maximum time to wait for a response")
	sandbox := flag.Bool("sandbox", false, "enable terminal sandbox mode")
	login := flag.Bool("login", false, "authenticate with Google")
	logout := flag.Bool("logout", false, "remove saved Google credentials")
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Println("agy-open", version)
		return
	}
	if *login {
		if err := (&auth.Manager{}).Login(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		fmt.Println("Google login complete.")
		return
	}
	if *logout {
		if err := (&auth.Manager{}).Logout(); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		fmt.Println("Google credentials removed.")
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
	if *model != "" {
		cfg.Model = *model
	}
	if *effort != "" {
		cfg.Effort = *effort
	}
	if *danger {
		cfg.ApprovalMode = "auto"
	}

	root, err := filepath.Abs(*workspace)
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
	_ = agentName
	_ = sandbox

	if prompt != "" {
		if *inputFormat == "stream-json" {
			fmt.Fprintln(os.Stderr, "agy: stream-json input cannot be combined with -p")
			os.Exit(2)
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), *printTimeout)
		defer cancel()
		var buf bytes.Buffer
		runErr := a.RunContext(ctx, prompt, &buf)
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
			runErr := a.RunContext(ctx, content, &buf)
			cancel()
			emitHeadless("stream-json", a.SessionID(), buf.String(), runErr, start, a)
		}
		if err := sc.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		return
	}

	if tui.IsTerminal() {
		ui := tui.New(a)
		if err := ui.Run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		return
	}
	if err := a.Repl(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
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
