package main

import (
	"context"
	"fmt"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
)

func main() {
	prompt := flag.String("p", "", "run one prompt and exit")
	model := flag.String("model", "", "Gemini model name")
	workspace := flag.String("workspace", ".", "workspace directory")
	auto := flag.Bool("dangerously-skip-permissions", false, "allow writes and shell commands without approval")
	oauthClient:=flag.String("oauth-client","","Google OAuth desktop client_secret.json")
	login:=flag.Bool("login",false,"authenticate with Google OAuth")
	logout:=flag.Bool("logout",false,"remove saved Google OAuth credentials")
	flag.Parse()

	if *login { if strings.TrimSpace(*oauthClient)=="" { fmt.Fprintln(os.Stderr,"usage: agy --login --oauth-client client_secret.json"); os.Exit(2) }; if err:=(&auth.Manager{}).Login(context.Background(),*oauthClient); err!=nil { fmt.Fprintln(os.Stderr,"agy:",err); os.Exit(1) }; fmt.Println("Google OAuth login complete."); return }
	if *logout { if err:=(&auth.Manager{}).Logout(); err!=nil { fmt.Fprintln(os.Stderr,"agy:",err); os.Exit(1) }; fmt.Println("Google OAuth credentials removed."); return }

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *auto {
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

	if strings.TrimSpace(*prompt) != "" {
		if err := a.Run(*prompt, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "agy:", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("agy-open — terminal-first AI coding agent")
	fmt.Println("workspace:", root)
	fmt.Println("type /help for commands; /quit exits")
	fmt.Println()

	if err := a.Repl(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agy:", err)
		os.Exit(1)
	}
}
