package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
)

type UI struct {
	agent   *agent.Agent
	lines   []string
	input   []rune
	cursor  int
	scroll  int
	working bool
	exit    bool
	reader  *bufio.Reader
}

func New(a *agent.Agent) *UI {
	return &UI{agent: a, reader: bufio.NewReader(os.Stdin)}
}

func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (u *UI) Run(ctx context.Context) error {
	state, err := rawMode()
	if err != nil {
		return err
	}
	defer state.restore()

	fmt.Print("[?1049h[?25l")
	defer fmt.Print("[?25h[?1049l")

	u.add("agy-open  ·  terminal agent")
	u.add("workspace: " + u.agent.WorkspaceRoot())
	u.add("model: " + u.agent.Model())
	u.add("")
	u.add("Enter prompt · / for commands · Ctrl+D exits · Ctrl+L clears screen")
	u.render()

	for !u.exit {
		key, err := u.readKey()
		if err != nil {
			return err
		}
		switch key {
		case "ENTER":
			u.submit(ctx)
		case "CTRL-C":
			u.input = nil
			u.cursor = 0
			u.render()
		case "CTRL-D":
			if len(u.input) == 0 {
				return nil
			}
			u.input = nil
			u.cursor = 0
			u.render()
		case "CTRL-L":
			u.render()
		case "ESC":
			u.input = nil
			u.cursor = 0
			u.render()
		case "BACKSPACE":
			if u.cursor > 0 {
				u.input = append(u.input[:u.cursor-1], u.input[u.cursor:]...)
				u.cursor--
			}
			u.render()
		case "LEFT":
			if u.cursor > 0 {
				u.cursor--
			}
			u.render()
		case "RIGHT":
			if u.cursor < len(u.input) {
				u.cursor++
			}
			u.render()
		case "UP":
			for i := len(u.lines) - 1; i >= 0; i-- {
				if strings.HasPrefix(u.lines[i], "you › ") {
					u.input = []rune(strings.TrimPrefix(u.lines[i], "you › "))
					u.cursor = len(u.input)
					break
				}
			}
			u.render()
		case "PUP":
			u.scroll += 5
			u.render()
		case "PDOWN":
			if u.scroll > 5 {
				u.scroll -= 5
			} else {
				u.scroll = 0
			}
			u.render()
		default:
			if strings.HasPrefix(key, "RUNE:") {
				r := []rune(strings.TrimPrefix(key, "RUNE:"))
				if len(r) == 1 {
					u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
					u.cursor++
				}
				u.render()
			}
		}
	}
	return nil
}

func (u *UI) submit(ctx context.Context) {
	text := strings.TrimSpace(string(u.input))
	if text == "" {
		return
	}
	u.input = nil
	u.cursor = 0

	if strings.HasPrefix(text, "/") {
		u.command(text)
		u.render()
		return
	}

	u.add("you › " + text)
	u.working = true
	u.render()

	var buf bytes.Buffer
	u.agent.SetConfirm(func(action, target string) bool {
		return u.confirm(action, target)
	})
	err := u.agent.RunContext(ctx, text, &buf)
	u.working = false

	if err != nil {
		u.add("error: " + err.Error())
	} else if s := strings.TrimSpace(buf.String()); s != "" {
		u.addBlock("agy ›", s)
	}
	u.render()
}

func (u *UI) command(raw string) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return
	}
	cmd := parts[0]
	arg := strings.TrimSpace(strings.TrimPrefix(raw, cmd))

	switch cmd {
	case "/help":
		u.addBlock("help", "/add-dir /agents /boost /btw /clear /config /context /copy /credits /diff /exit /fast /feedback /fork /help /hooks /keybindings /logout /mcp /model /open /permissions /planning /plugin /rename /remote-control /resume /rewind /skills /statusline /tasks /title /usage /voice")
	case "/exit", "/quit":
		u.exit = true
	case "/clear", "/new":
		u.agent.Clear()
		u.lines = nil
	case "/model":
		if arg == "" {
			u.add("model: " + u.agent.Model())
		} else if err := u.agent.SetModel(arg); err != nil {
			u.add("error: " + err.Error())
		} else {
			u.add("model: " + u.agent.Model())
		}
	case "/effort":
		if arg == "" {
			u.add("effort: medium")
		} else if err := u.agent.SetEffort(arg); err != nil {
			u.add("error: " + err.Error())
		} else {
			u.add("effort: " + arg)
		}
	case "/permissions":
		if arg == "" {
			u.add("permissions: " + u.agent.ApprovalMode())
		} else if err := u.agent.SetApproval(arg); err != nil {
			u.add("error: " + err.Error())
		} else {
			u.add("permissions: " + u.agent.ApprovalMode())
		}
	case "/ask":
		_ = u.agent.SetApproval("request-review")
		u.add("permissions: request-review")
	case "/approve":
		_ = u.agent.SetApproval("always-proceed")
		u.add("permissions: always-proceed")
	case "/fast":
		_ = u.agent.SetEffort("low")
		u.add("effort: low")
	case "/planning":
		_ = u.agent.SetEffort("high")
		u.add("effort: high")
	case "/context":
		u.add(fmt.Sprintf("context: ~%d characters", u.agent.ContextChars()))
	case "/usage", "/quota", "/credits":
		u.add("usage: token/quota telemetry is not exposed by this backend")
	case "/resume", "/switch", "/conversation":
		u.add("resume: session picker is the next parity step")
	case "/diff":
		u.shellView("git diff --no-ext-diff")
	case "/open":
		if arg == "" {
			u.add("usage: /open <path>")
		} else {
			u.open(arg)
		}
	case "/logout":
		if err := (&auth.Manager{}).Logout(); err != nil {
			u.add("logout: " + err.Error())
		} else {
			u.add("logged out")
		}
	case "/title", "/statusline":
		u.add(cmd + ": customization is planned")
	default:
		u.add(cmd + ": recognized by the compatibility TUI; feature not implemented yet")
	}
}

func (u *UI) add(s string) {
	u.lines = append(u.lines, s)
}

func (u *UI) addBlock(title, s string) {
	u.add(title)
	u.lines = append(u.lines, strings.Split(strings.TrimRight(s, "\n"), "\n")...)
}

func (u *UI) confirm(action, target string) bool {
	u.add(fmt.Sprintf("permission requested: %s %s", action, target))
	u.render()
	fmt.Print("[2KAllow? [y/N] ")

	for {
		key, err := u.readKey()
		if err != nil {
			return false
		}
		switch key {
		case "RUNE:y", "RUNE:Y":
			u.add("approved")
			return true
		case "RUNE:n", "RUNE:N", "ENTER":
			u.add("denied")
			return false
		}
	}
}

func (u *UI) render() {
	cols, rows := size()
	if rows < 8 {
		rows = 8
	}
	body := rows - 4

	all := append([]string{}, u.lines...)
	if u.working {
		all = append(all, "agy › working...")
	}

	start := len(all) - body - u.scroll
	if start < 0 {
		start = 0
	}
	end := len(all) - u.scroll
	if end < 0 {
		end = 0
	}
	if end > len(all) {
		end = len(all)
	}

	fmt.Print("[H[2J")
	fmt.Println("agy-open  ·  terminal agent")
	fmt.Printf("model: %-24s mode: %-13s\\n", u.agent.Model(), u.agent.ApprovalMode())
	fmt.Println(strings.Repeat("─", max(1, cols)))

	for i := start; i < end; i++ {
		for _, line := range wrap(all[i], cols) {
			fmt.Println(line)
		}
	}
	for i := end - start; i < body; i++ {
		fmt.Println()
	}

	fmt.Println(strings.Repeat("─", max(1, cols)))
	fmt.Print("› " + string(u.input))
	fmt.Printf("[%dG", 3+u.cursor)
}

func (u *UI) shellView(command string) {
	cmd := exec.Command("sh", "-lc", command)
	cmd.Dir = u.agent.WorkspaceRoot()
	b, err := cmd.CombinedOutput()
	if err != nil {
		u.add("command: " + err.Error())
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		s = "(no changes)"
	}
	u.addBlock("diff", s)
}

func (u *UI) open(path string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func (u *UI) readKey() (string, error) {
	r, _, err := u.reader.ReadRune()
	if err != nil {
		return "", err
	}

	switch r {
	case '', '
':
		return "ENTER", nil
	case 3:
		return "CTRL-C", nil
	case 4:
		return "CTRL-D", nil
	case 8, 127:
		return "BACKSPACE", nil
	case 12:
		return "CTRL-L", nil
	case 27:
		r2, _, e := u.reader.ReadRune()
		if e != nil {
			return "ESC", nil
		}
		if r2 != '[' {
			return "ESC", nil
		}
		r3, _, e := u.reader.ReadRune()
		if e != nil {
			return "ESC", nil
		}
		switch r3 {
		case 'A':
			return "UP", nil
		case 'B':
			return "DOWN", nil
		case 'C':
			return "RIGHT", nil
		case 'D':
			return "LEFT", nil
		case '5':
			_, _, _ = u.reader.ReadRune()
			return "PUP", nil
		case '6':
			_, _, _ = u.reader.ReadRune()
			return "PDOWN", nil
		default:
			return "ESC", nil
		}
	default:
		return "RUNE:" + string(r), nil
	}
}

type rawState struct {
	saved string
}

func rawMode() (rawState, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	b, err := cmd.Output()
	if err != nil {
		return rawState{}, err
	}
	saved := strings.TrimSpace(string(b))
	mode := exec.Command("stty", "-icanon", "-echo", "min", "1", "time", "0")
	mode.Stdin = os.Stdin
	if err := mode.Run(); err != nil {
		return rawState{}, err
	}
	return rawState{saved: saved}, nil
}

func (s rawState) restore() {
	if s.saved != "" {
		cmd := exec.Command("stty", s.saved)
		cmd.Stdin = os.Stdin
		_ = cmd.Run()
	} else {
		cmd := exec.Command("stty", "sane")
		cmd.Stdin = os.Stdin
		_ = cmd.Run()
	}
}

func size() (int, int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	b, err := cmd.Output()
	if err != nil {
		return 80, 24
	}
	p := strings.Fields(string(b))
	if len(p) != 2 {
		return 80, 24
	}
	r, _ := strconv.Atoi(p[0])
	c, _ := strconv.Atoi(p[1])
	return c, r
}

func wrap(s string, n int) []string {
	if n < 1 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) == 0 {
		return []string{""}
	}
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
