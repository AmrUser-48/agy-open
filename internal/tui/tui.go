package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/session"
)

var commandNames = []string{
	"/add-dir",
	"/agents",
	"/boost",
	"/artifact",
	"/btw",
	"/clear",
	"/config",
	"/context",
	"/copy",
	"/credits",
	"/diff",
	"/exit",
	"/fast",
	"/feedback",
	"/fork",
	"/help",
	"/hooks",
	"/keybindings",
	"/logout",
	"/mcp",
	"/model",
	"/open",
	"/permissions",
	"/planning",
	"/plugin",
	"/rename",
	"/remote-control",
	"/resume",
	"/rewind",
	"/skills",
	"/statusline",
	"/tasks",
	"/teamwork-preview",
	"/title",
	"/usage",
	"/voice",
}

type UI struct {
	agent          *agent.Agent
	lines          []string
	input          []rune
	cursor         int
	scroll         int
	working        bool
	exit           bool
	reader         *bufio.Reader
	history        []string
	historyIndex   int
	commandIndex   int
	showCommandBox bool
	showStatus     bool
	titleEnabled   bool
	showTrajectory bool
	picker         []session.Entry
	pickerIndex    int
}

func New(a *agent.Agent) *UI {
	return &UI{
		agent:        a,
		reader:       bufio.NewReader(os.Stdin),
		historyIndex: -1,
		showStatus:   true,
		titleEnabled: true,
	}
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

	enterAltScreen()
	defer leaveAltScreen()
	u.updateModes()
	u.setTitle()
	u.add("agy-open  ·  terminal agent")
	u.add("workspace: " + u.agent.WorkspaceRoot())
	u.add("model: " + u.agent.Model())
	u.add("")
	u.add("Enter prompt · / commands · @ paths · ! shell · Ctrl+D exits")
	u.render()

	for !u.exit {
		key, err := u.readKey()
		if err != nil {
			return err
		}
		if u.picker != nil {
			u.handlePicker(key, ctx)
			continue
		}
		switch key {
		case "ENTER":
			u.submit(ctx)
		case "TAB":
			u.complete()
		case "CTRL-C":
			u.input = nil
			u.cursor = 0
			u.historyIndex = -1
			u.closeCommandBox()
			u.render()
		case "CTRL-D":
			if len(u.input) == 0 {
				return nil
			}
			u.deleteForward()
			u.render()
		case "CTRL-L":
			u.scroll = 0
			u.render()
		case "ESC":
			if u.showCommandBox {
				u.closeCommandBox()
			} else {
				u.input = nil
				u.cursor = 0
			}
			u.render()
		case "BACKSPACE":
			u.deleteBackward()
			u.updateModes()
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
		case "CTRL-A":
			u.cursor = 0
			u.render()
		case "CTRL-E":
			u.cursor = len(u.input)
			u.render()
		case "CTRL-G":
			if next, err := u.externalEditor(state); err != nil {
				u.add("editor: " + err.Error())
				u.render()
			} else {
				state = next
				u.render()
			}
		case "CTRL-V":
			u.paste()
			u.render()
		case "CTRL-O":
			u.showTrajectory = !u.showTrajectory
			u.add(fmt.Sprintf("trajectory display: %s", onOff(u.showTrajectory)))
			u.render()
		case "CTRL-R":
			u.shellView("git diff --no-ext-diff")
			u.render()
		case "CTRL-Z":
			u.add("undo: prompt edit reverted")
			u.input = nil
			u.cursor = 0
			u.render()
		case "UP":
			u.navigateHistory(-1)
			u.render()
		case "DOWN":
			u.navigateHistory(1)
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
					u.pushUndo()
					u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
					u.cursor++
					u.historyIndex = -1
				}
				u.updateModes()
				u.render()
			}
		}
	}
	return nil
}

func (u *UI) submit(ctx context.Context) {
	raw := string(u.input)
	text := strings.TrimSpace(raw)
	if text == "" {
		return
	}
	u.input = nil
	u.cursor = 0
	u.historyIndex = -1
	u.closeCommandBox()

	if strings.HasPrefix(text, "/") {
		u.command(text, ctx)
		u.render()
		return
	}

	if strings.HasPrefix(text, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(text, "!"))
		if command == "" {
			return
		}
		u.add("you › !" + command)
		u.runDirectShell(ctx, command)
		u.render()
		return
	}

	u.history = append(u.history, text)
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

func (u *UI) add(line string) {
	u.lines = append(u.lines, line)
}

func (u *UI) command(raw string, ctx context.Context) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return
	}
	cmd := parts[0]
	arg := strings.TrimSpace(strings.TrimPrefix(raw, cmd))

	switch cmd {
	case "/help":
		u.addBlock("help", strings.Join(commandNames, " "))
	case "/exit", "/quit":
		u.exit = true
	case "/clear", "/new":
		u.agent.Clear()
		u.lines = nil
	case "/model":
		if arg == "" {
			ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			models, err := u.agent.ListModels(ctx2)
			if err != nil {
				u.add("model: " + err.Error())
			} else if len(models) == 0 {
				u.add("model: no models returned")
			} else {
				u.addBlock("models", strings.Join(models, "\n"))
			}
		} else if err := u.agent.SetModel(arg); err != nil {
			u.add("model: " + err.Error())
		} else {
			u.setTitle()
			u.add("model: " + u.agent.Model())
		}
	case "/effort":
		if arg == "" {
			u.add("effort: " + u.agent.Effort())
		} else if err := u.agent.SetEffort(arg); err != nil {
			u.add("effort: " + err.Error())
		} else {
			u.add("effort: " + u.agent.Effort())
		}
	case "/permissions":
		if arg == "" {
			u.add("permissions: " + u.agent.ApprovalMode())
			u.add("available: request-review, proceed-in-sandbox, always-proceed, strict")
		} else if err := u.agent.SetApproval(arg); err != nil {
			u.add("permissions: " + err.Error())
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
	case "/boost":
		if arg == "" {
			u.add("usage: /boost <task>")
			break
		}
		old := u.agent.Effort()
		_ = u.agent.SetEffort("high")
		defer u.agent.SetEffort(old)
		u.runPrompt(ctx, arg, "boost")
	case "/teamwork-preview", "/teamwork":
		if arg == "" {
			u.add("usage: /teamwork-preview <task>")
			break
		}
		u.add("teamwork: compatibility mode uses the single configured agent")
		u.runPrompt(ctx, arg, "teamwork")
	case "/btw":
		u.add("btw: side-channel conversation is not isolated yet")
	case "/context":
		u.add(fmt.Sprintf("context: ~%d characters", u.agent.ContextChars()))
	case "/usage", "/quota", "/credits":
		u.add("usage: remote quota/credit telemetry is not exposed by this Gemini backend")
	case "/resume", "/switch", "/conversation":
		if arg != "" {
			if err := u.agent.ResumeSession(arg); err != nil {
				u.add("resume: " + err.Error())
			} else {
				u.add("resumed: " + arg)
			}
		} else {
			u.openPicker()
		}
	case "/rewind", "/undo":
		if err := u.agent.Rewind(); err != nil {
			u.add("rewind: " + err.Error())
		} else {
			u.add("rewound one conversation turn")
		}
	case "/diff":
		u.shellView("git diff --no-ext-diff")
	case "/open":
		if arg == "" {
			u.add("usage: /open <path>")
		} else if err := u.openPath(stateInfo{path: arg}); err != nil {
			u.add("open: " + err.Error())
		}
	case "/copy":
		u.copyLast()
	case "/logout":
		if err := (&auth.Manager{}).Logout(); err != nil {
			u.add("logout: " + err.Error())
		} else {
			u.add("logged out")
		}
	case "/config", "/settings":
		u.addBlock("config", fmt.Sprintf("model=%s\neffort=%s\npermissions=%s\nworkspace=%s", u.agent.Model(), u.agent.Effort(), u.agent.ApprovalMode(), u.agent.WorkspaceRoot()))
	case "/keybindings":
		u.addBlock("keybindings", "Enter submit · Esc cancel · Ctrl+C cancel · Ctrl+D exit/delete · Ctrl+L clear · Ctrl+G editor · Ctrl+V paste · Ctrl+O trajectory · Ctrl+R diff · Ctrl+A/E cursor · Up/Down history · PgUp/PgDn scroll · Tab autocomplete")
	case "/statusline":
		u.showStatus = !u.showStatus
		u.add("statusline: " + onOff(u.showStatus))
	case "/title":
		switch strings.ToLower(arg) {
		case "on":
			u.titleEnabled = true
		case "off":
			u.titleEnabled = false
		default:
			u.titleEnabled = !u.titleEnabled
		}
		u.setTitle()
		u.add("title: " + onOff(u.titleEnabled))
	case "/agents":
		u.add("agents: default")
	case "/artifact":
		u.add("artifact: no artifact-review backend configured")
	case "/hooks":
		u.add("hooks: no hook runner configured")
	case "/mcp":
		u.add("mcp: no MCP server manager configured")
	case "/plugin", "/plugins":
		u.add("plugins: no plugin manager configured")
	case "/skills":
		u.add("skills: no skill registry configured")
	case "/tasks":
		u.add("tasks: no background task runner configured")
	case "/remote-control":
		u.add("remote-control: not configured")
	case "/voice", "/record":
		u.add("voice: not available in the lightweight Linux build")
	case "/feedback":
		u.add("feedback: use the project issue tracker")
	case "/rename":
		u.add("rename: conversation renaming is not implemented yet")
	case "/fork", "/branch":
		u.agent.Clear()
		u.add("fork: started a new conversation context")
	case "/add-dir":
		u.add("add-dir: multiple workspace roots are not implemented yet")
	default:
		u.add("unknown command: " + cmd)
	}
}

func (u *UI) addBlock(title, content string) {
	u.add(title)
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		u.add(line)
	}
}

func (u *UI) runPrompt(ctx context.Context, prompt, label string) {
	u.add("you › " + prompt)
	u.working = true
	u.render()
	var buf bytes.Buffer
	u.agent.SetConfirm(func(action, target string) bool { return u.confirm(action, target) })
	err := u.agent.RunContext(ctx, prompt, &buf)
	u.working = false
	if err != nil {
		u.add(label+": "+err.Error())
		return
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		u.addBlock("agy ›", s)
	}
}

func (u *UI) runDirectShell(ctx context.Context, command string) {
	if !u.confirm("shell", command) {
		u.add("shell: denied")
		return
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-lc", command)
	cmd.Dir = u.agent.WorkspaceRoot()
	b, err := cmd.CombinedOutput()
	if err != nil {
		u.add("shell: " + err.Error())
	}
	s := strings.TrimSpace(string(b))
	if s != "" {
		u.addBlock("$ "+command, s)
	}
}

func (u *UI) confirm(action, target string) bool {
	u.add(fmt.Sprintf("permission requested: %s %s", action, target))
	u.render()
	fmt.Print("\x1b[2K\rAllow? [y/N] ")
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

func (u *UI) openPicker() {
	entries, err := u.agent.SessionList(12)
	if err != nil {
		u.add("resume: " + err.Error())
		return
	}
	if len(entries) == 0 {
		u.add("resume: no saved conversations")
		return
	}
	u.picker = entries
	u.pickerIndex = 0
}

func (u *UI) handlePicker(key string, ctx context.Context) {
	switch key {
	case "ESC":
		u.picker = nil
	case "UP":
		if u.pickerIndex > 0 { u.pickerIndex-- }
	case "DOWN":
		if u.pickerIndex+1 < len(u.picker) { u.pickerIndex++ }
	case "ENTER":
		entry := u.picker[u.pickerIndex]
		if err := u.agent.ResumeSession(entry.ID); err != nil {
			u.add("resume: " + err.Error())
		} else {
			u.add("resumed: " + entry.ID)
		}
		u.picker = nil
	case "CTRL-C":
		u.picker = nil
	}
	u.render()
}

func (u *UI) complete() {
	if u.showCommandBox {
		u.matchCommands(true)
		return
	}
	text := string(u.input)
	if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
		u.showCommandBox = true
		u.matchCommands(true)
		return
	}

	idx := strings.LastIndex(text[:u.cursor], "@")
	if idx < 0 {
		return
	}
	prefix := text[idx+1:u.cursor]
	matches, _ := filepath.Glob(filepath.Join(u.agent.WorkspaceRoot(), prefix+"*"))
	if len(matches) == 1 {
		rel, _ := filepath.Rel(u.agent.WorkspaceRoot(), matches[0])
		replacement := "@" + rel + " "
		u.input = append(append([]rune{}, []rune(text[:idx])...), []rune(replacement+text[u.cursor:])...)
		u.cursor = idx + len([]rune(replacement))
		return
	}
	if len(matches) > 0 {
		u.addBlock("path suggestions", strings.Join(matches, "\n"))
	}
}

func (u *UI) matchCommands(cycle bool) {
	prefix := string(u.input)
	if !strings.HasPrefix(prefix, "/") {
		u.closeCommandBox()
		return
	}
	var matches []string
	for _, name := range commandNames {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		u.closeCommandBox()
		return
	}
	if cycle {
		u.commandIndex++
		if u.commandIndex >= len(matches) { u.commandIndex = 0 }
	}
	u.showCommandBox = true
}

func (u *UI) closeCommandBox() {
	u.showCommandBox = false
	u.commandIndex = 0
}

func (u *UI) updateModes() {
	text := string(u.input)
	u.showCommandBox = strings.HasPrefix(text, "/") && !strings.Contains(text, " ")
	if u.showCommandBox {
		u.matchCommands(false)
	}
}

func (u *UI) navigateHistory(direction int) {
	if len(u.history) == 0 {
		return
	}
	if direction < 0 {
		if u.historyIndex < len(u.history)-1 {
			u.historyIndex++
		}
		u.input = []rune(u.history[len(u.history)-1-u.historyIndex])
	} else {
		if u.historyIndex <= 0 {
			u.historyIndex = -1
			u.input = nil
			u.cursor = 0
			return
		}
		u.historyIndex--
		u.input = []rune(u.history[len(u.history)-1-u.historyIndex])
	}
	u.cursor = len(u.input)
	u.updateModes()
}

func (u *UI) pushUndo() {}

func (u *UI) deleteBackward() {
	if u.cursor == 0 { return }
	u.input = append(u.input[:u.cursor-1], u.input[u.cursor:]...)
	u.cursor--
}

func (u *UI) deleteForward() {
	if u.cursor >= len(u.input) { return }
	u.input = append(u.input[:u.cursor], u.input[u.cursor+1:]...)
}

func (u *UI) paste() {
	var commands = [][]string{
		{"wl-paste"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
	}
	for _, argv := range commands {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		b, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil { continue }
		r := []rune(strings.TrimSuffix(string(b), "\n"))
		u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
		u.cursor += len(r)
		return
	}
	u.add("paste: no clipboard utility found")
}

func (u *UI) copyLast() {
	text := strings.TrimSpace(u.agent.LastResponse())
	if text == "" {
		u.add("copy: no agent response")
		return
	}
	commands := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	for _, argv := range commands {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			u.add("copied last response")
			return
		}
	}
	u.add("copy: no clipboard utility found")
}

type stateInfo struct{ path string }

func (u *UI) openPath(info stateInfo) error {
	path := info.path
	if strings.TrimSpace(path) == "" { return fmt.Errorf("path is required") }
	return u.runExternalEditor(path)
}

func (u *UI) externalEditor(state rawState) (rawState, error) {
	path, err := os.CreateTemp("", "agy-prompt-*.txt")
	if err != nil { return state, err }
	name := path.Name()
	defer os.Remove(name)
	if _, err := path.WriteString(string(u.input)); err != nil { path.Close(); return state, err }
	_ = path.Close()

	state.restore()
	leaveAltScreen()
	fmt.Print("\x1b[?25h")
	cmd := exec.Command(editorName(), name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	enterAltScreen()
	fmt.Print("\x1b[?25l")

	next, err := rawMode()
	if err != nil { return state, err }
	if runErr != nil { return next, runErr }
	b, err := os.ReadFile(name)
	if err != nil { return next, err }
	u.input = []rune(string(b))
	u.cursor = len(u.input)
	return next, nil
}

func (u *UI) runExternalEditor(path string) error {
	restore := exec.Command("stty", "sane")
	restore.Stdin = os.Stdin
	_ = restore.Run()
	leaveAltScreen()
	fmt.Print("\x1b[?25h")
	cmd := exec.Command(editorName(), path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	enterAltScreen()
	fmt.Print("\x1b[?25l")
	if _, err := rawMode(); err != nil {
		return err
	}
	return runErr
}

func (u *UI) setTitle() {
	if !u.titleEnabled { fmt.Print("\x1b]0;\x07"); return }
	fmt.Printf("\x1b]0;agy-open — %s — %s\x07", u.agent.Model(), filepath.Base(u.agent.WorkspaceRoot()))
}

func (u *UI) shellView(command string) {
	cmd := exec.Command("sh", "-lc", command)
	cmd.Dir = u.agent.WorkspaceRoot()
	b, err := cmd.CombinedOutput()
	if err != nil {
		u.add("command: " + err.Error())
	}
	s := strings.TrimSpace(string(b))
	if s == "" { s = "(no changes)" }
	u.addBlock("diff", s)
}

func (u *UI) render() {
	cols, rows := size()
	if rows < 10 { rows = 10 }
	body := rows - 5
	all := append([]string{}, u.lines...)
	if u.working { all = append(all, "agy › working...") }
	if len(u.picker) > 0 {
		all = append(all, "")
		all = append(all, "┌─ conversations ─────────────────────────────────")
		for i, entry := range u.picker {
			prefix := "  "
			if i == u.pickerIndex { prefix = "› " }
			all = append(all, prefix+entry.ID)
		}
		all = append(all, "└────────────────────────────────────────────────")
	}
	if u.showCommandBox {
		all = append(all, "")
		all = append(all, "commands:")
		prefix := string(u.input)
		var shown int
		for _, name := range commandNames {
			if strings.HasPrefix(name, prefix) {
				all = append(all, "  "+name)
				shown++
				if shown >= 7 { break }
			}
		}
	}

	start := len(all) - body - u.scroll
	if start < 0 { start = 0 }
	end := len(all) - u.scroll
	if end < 0 { end = 0 }
	if end > len(all) { end = len(all) }

	fmt.Print("\x1b[H\x1b[2J")
	if u.showStatus {
		fmt.Printf("agy-open  ·  %s  ·  %s\n", u.agent.Model(), u.agent.ApprovalMode())
		fmt.Println(strings.Repeat("─", max(1, cols)))
	}
	for i := start; i < end; i++ {
		for _, line := range wrap(all[i], cols) { fmt.Println(line) }
	}
	used := end - start
	for i := used; i < body; i++ { fmt.Println() }
	fmt.Println(strings.Repeat("─", max(1, cols)))
	fmt.Print("› " + string(u.input))
	fmt.Printf("\x1b[%dG", 3+u.cursor)
}

func enterAltScreen() { fmt.Print("\x1b[?1049h\x1b[?25l") }
func leaveAltScreen() { fmt.Print("\x1b[?25h\x1b[?1049l") }

type rawState struct{ saved string }

func rawMode() (rawState, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	b, err := cmd.Output()
	if err != nil { return rawState{}, err }
	saved := strings.TrimSpace(string(b))
	mode := exec.Command("stty", "-icanon", "-echo", "min", "1", "time", "0")
	mode.Stdin = os.Stdin
	if err := mode.Run(); err != nil { return rawState{}, err }
	return rawState{saved:saved}, nil
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

func editorName() string {
	if e := os.Getenv("EDITOR"); e != "" { return e }
	return "vi"
}

func (u *UI) readKey() (string, error) {
	r, _, err := u.reader.ReadRune()
	if err != nil { return "", err }
	switch r {
	case '\r', '\n': return "ENTER", nil
	case '\t': return "TAB", nil
	case 1: return "CTRL-A", nil
	case 3: return "CTRL-C", nil
	case 4: return "CTRL-D", nil
	case 5: return "CTRL-E", nil
	case 7: return "CTRL-G", nil
	case 8, 127: return "BACKSPACE", nil
	case 12: return "CTRL-L", nil
	case 15: return "CTRL-O", nil
	case 18: return "CTRL-R", nil
	case 22: return "CTRL-V", nil
	case 26: return "CTRL-Z", nil
	case 27:
		r2, _, e := u.reader.ReadRune()
		if e != nil { return "ESC", nil }
		if r2 != '[' { return "ESC", nil }
		r3, _, e := u.reader.ReadRune()
		if e != nil { return "ESC", nil }
		switch r3 {
		case 'A': return "UP", nil
		case 'B': return "DOWN", nil
		case 'C': return "RIGHT", nil
		case 'D': return "LEFT", nil
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

func size() (int, int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	b, err := cmd.Output()
	if err != nil { return 80, 24 }
	p := strings.Fields(string(b))
	if len(p) != 2 { return 80, 24 }
	r, _ := strconv.Atoi(p[0])
	c, _ := strconv.Atoi(p[1])
	return c, r
}

func wrap(s string, n int) []string {
	if n < 1 { return []string{s} }
	r := []rune(s)
	if len(r) == 0 { return []string{""} }
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}

func onOff(v bool) string {
	if v { return "on" }
	return "off"
}

func max(a, b int) int {
	if a > b { return a }
	return b
}
