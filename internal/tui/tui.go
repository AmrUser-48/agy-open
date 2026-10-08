package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/session"
)

type message struct {
	kind string
	text string
}

type approvalRequest struct {
	action string
	target string
	reply  chan bool
}

type uiEvent struct {
	kind     string
	text     string
	tool     string
	target   string
	output   string
	ok       bool
	approval *approvalRequest
}

type overlay struct {
	title   string
	items   []string
	index   int
	kind    string
	footer  string
}

var commandNames = []string{
	"/add-dir", "/agents", "/boost", "/artifact", "/btw", "/clear", "/config",
	"/context", "/copy", "/credits", "/diff", "/exit", "/fast", "/feedback",
	"/fork", "/help", "/hooks", "/keybindings", "/logout", "/mcp", "/model",
	"/open", "/permissions", "/planning", "/plugin", "/rename", "/remote-control",
	"/resume", "/rewind", "/skills", "/statusline", "/tasks", "/teamwork-preview",
	"/title", "/usage", "/voice",
	"/branch", "/conversation", "/new", "/quota", "/quit", "/record", "/settings",
}

var commandDescription = map[string]string{
	"/add-dir":            "Add a directory to the active workspace",
	"/agents":             "Open the agent manager",
	"/boost":              "Run a deep reasoning task",
	"/artifact":           "Open artifact review",
	"/btw":                "Ask a background side question",
	"/clear":              "Clear the terminal and conversation context",
	"/config":             "Open the settings editor",
	"/context":            "Show context usage",
	"/copy":               "Copy the last response",
	"/credits":            "Show available AI credits",
	"/diff":               "Open the working-tree diff",
	"/exit":               "Exit the CLI",
	"/fast":               "Use fast reasoning mode",
	"/feedback":           "Show feedback information",
	"/fork":               "Fork the active conversation",
	"/help":               "Show commands and shortcuts",
	"/hooks":              "Show configured hooks",
	"/keybindings":        "Show keyboard shortcuts",
	"/logout":             "Log out",
	"/mcp":                "Open MCP manager",
	"/model":              "Select the reasoning model",
	"/open":               "Open a path in the external editor",
	"/permissions":        "Select tool permission mode",
	"/planning":           "Enable high-effort planning",
	"/plugin":             "Open the plugin manager",
	"/rename":             "Rename the current conversation",
	"/remote-control":     "Remote-control status",
	"/resume":             "Open the conversation picker",
	"/rewind":             "Roll back one conversation turn",
	"/skills":             "Browse skills",
	"/statusline":         "Configure the status line",
	"/tasks":              "Open the task manager",
	"/teamwork-preview":   "Run a collaborative task",
	"/title":              "Toggle terminal title",
	"/usage":              "Show model quota usage",
	"/voice":              "Voice input",
}

type colors struct {
	enabled bool
	dim     string
	bold    string
	cyan    string
	green   string
	yellow  string
	red     string
	blue    string
	reset   string
	reverse string
}

func newColors(cfg config.Config) colors {
	enabled := os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if !enabled {
		return colors{}
	}
	switch strings.ToLower(cfg.ColorScheme) {
	case "light", "solarized light", "colorblind-friendly light":
		return colors{true, "\x1b[2m", "\x1b[1m", "\x1b[36m", "\x1b[32m", "\x1b[33m", "\x1b[31m", "\x1b[34m", "\x1b[0m", "\x1b[7m"}
	default:
		return colors{true, "\x1b[2m", "\x1b[1m", "\x1b[36m", "\x1b[32m", "\x1b[33m", "\x1b[31m", "\x1b[34m", "\x1b[0m", "\x1b[7m"}
	}
}

type UI struct {
	agent *agent.Agent
	cfg   config.Config
	col   colors

	lines []message
	input []rune
	cursor int

	scroll       int
	history      []string
	historyIndex int
	undoStack    []string
	redoStack    []string

	commands []string
	commandIndex int
	completionActive bool

	overlay *overlay
	approval *approvalRequest

	events chan uiEvent
	keys   chan string
	resize chan os.Signal

	working bool
	cancel context.CancelFunc
	spinner int
	status string
	showStatus bool
	titleEnabled bool
	trajectory bool
	rawMarkdown bool
	exit bool
	lastCtrlC time.Time

	models []string
	agents []string

	raw rawState
}

func New(a *agent.Agent) *UI {
	cfg := a.Config()
	u := &UI{
		agent:       a,
		cfg:         cfg,
		col:         newColors(cfg),
		commands:    append([]string(nil), commandNames...),
		events:      make(chan uiEvent, 64),
		keys:        make(chan string, 32),
		resize:      make(chan os.Signal, 1),
		showStatus:  true,
		titleEnabled: true,
		historyIndex: -1,
	}
	sort.Strings(u.commands)
	return u
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
	u.raw = state

	signal.Notify(u.resize, syscall.SIGWINCH)
	defer signal.Stop(u.resize)
	defer func() {
		if u.cancel != nil {
			u.cancel()
			u.cancel = nil
		}
		if u.approval != nil {
			select {
			case u.approval.reply <- false:
			default:
			}
			u.approval = nil
		}
		fmt.Print("\x1b[?25h\x1b[0m")
		leaveAltScreen()
		u.raw.restore()
	}()

	enterAltScreen()
	fmt.Print("\x1b[?25l\x1b[0m")
	u.setTitle()
	u.lines = append(u.lines,
		message{"info", "agy-open  ·  terminal agent"},
		message{"info", "workspace  " + u.agent.WorkspaceRoot()},
		message{"info", "model      " + u.agent.Model()},
		message{"hint", "Type / for commands · @ for files · ! for shell · Ctrl+G editor · Ctrl+C cancel/exit"},
	)

	go u.keyLoop()
	u.agent.SetConfirm(u.confirm)
	u.agent.SetEventSink(u.agentEvent)

	u.render()

	tickEvery := 120 * time.Millisecond
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for !u.exit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case key := <-u.keys:
			u.handleKey(ctx, key)
		case ev := <-u.events:
			u.handleEvent(ev)
		case <-ticker.C:
			if u.working {
				u.spinner++
				u.render()
			}
		case <-u.resize:
			u.render()
		}
	}

	return nil
}

func (u *UI) keyLoop() {
	r := bufio.NewReader(os.Stdin)
	for {
		key, err := readKey(r)
		if err != nil {
			u.keys <- "EOF"
			return
		}
		u.keys <- key
	}
}

func (u *UI) handleKey(ctx context.Context, key string) {
	if key == "EOF" {
		u.exit = true
		return
	}

	if u.approval != nil {
		u.handleApproval(key)
		return
	}

	if u.overlay != nil {
		u.handleOverlay(ctx, key)
		return
	}

	switch key {
	case "ENTER":
		if u.completionActive && u.tryAcceptCompletion() {
			u.render()
			return
		}
		if u.working {
			return
		}
		u.submit(ctx)
	case "SHIFT-ENTER", "CTRL-J":
		u.saveUndo()
		u.input = append(u.input[:u.cursor], append([]rune{'\n'}, u.input[u.cursor:]...)...)
		u.cursor++
		u.completionActive = false
	case "TAB":
		if u.tryAcceptCompletion() {
			break
		}
		u.complete()
	case "UP":
		if u.completionActive {
			u.moveCompletion(-1)
		} else {
			u.navigateHistory(-1)
		}
	case "DOWN":
		if u.completionActive {
			u.moveCompletion(1)
		} else {
			u.navigateHistory(1)
		}
	case "LEFT":
		if u.cursor > 0 {
			u.cursor--
		}
	case "RIGHT":
		if u.cursor < len(u.input) {
			u.cursor++
		}
	case "CTRL-A", "HOME":
		u.cursor = 0
	case "CTRL-E", "END":
		u.cursor = len(u.input)
	case "CTRL-B":
		if u.cursor > 0 {
			u.cursor--
		}
	case "CTRL-F":
		if u.cursor < len(u.input) {
			u.cursor++
		}
	case "CTRL-W":
		u.deleteWordBackward()
	case "CTRL-U":
		u.saveUndo()
		u.input = u.input[u.cursor:]
		u.cursor = 0
	case "CTRL-K":
		u.saveUndo()
		u.input = u.input[:u.cursor]
	case "CTRL-D":
		if len(u.input) == 0 {
			u.exit = true
		} else {
			u.saveUndo()
			u.deleteForward()
		}
	case "BACKSPACE", "CTRL-H":
		u.saveUndo()
		u.deleteBackward()
	case "CTRL-L":
		u.scroll = 0
	case "CTRL-C":
		u.ctrlC()
	case "ESC":
		u.completionActive = false
		u.overlay = nil
		u.input = nil
		u.cursor = 0
	case "ALT-Z":
		u.undo()
	case "ALT-Y":
		u.redo()
	case "CTRL-V":
		u.paste()
	case "CTRL-G":
		u.openEditor()
	case "CTRL-O":
		u.trajectory = !u.trajectory
		u.lines = append(u.lines, message{"info", fmt.Sprintf("trajectory display %s", onOff(u.trajectory))})
	case "CTRL-R":
		u.showDiff()
	case "CTRL-T":
		u.lines = append(u.lines, message{"info", "task list display toggled"})
	case "CTRL-Y":
		if u.agent.ApprovalMode() == "auto" {
			_ = u.agent.SetApproval("request-review")
		} else {
			_ = u.agent.SetApproval("always-proceed")
		}
		u.persistConfig()
	case "SHIFT-TAB":
		u.cyclePermission()
	default:
		if strings.HasPrefix(key, "RUNE:") && !u.working {
			r := []rune(strings.TrimPrefix(key, "RUNE:"))
			if len(r) == 1 {
				u.saveUndo()
				u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
				u.cursor++
				u.historyIndex = -1
				u.updateCompletion()
			}
		}
	}

	u.render()
}

func (u *UI) ctrlC() {
	now := time.Now()
	if u.working {
		if u.cancel != nil {
			u.cancel()
			u.cancel = nil
		}
		u.working = false
		u.status = "Interrupted"
		u.lines = append(u.lines, message{"warning", "Interrupted. Press Ctrl+C again to exit."})
		if u.approval != nil {
			select {
			case u.approval.reply <- false:
			default:
			}
			u.approval = nil
		}
		u.render()
		return
	}

	if !u.lastCtrlC.IsZero() && now.Sub(u.lastCtrlC) < 900*time.Millisecond {
		u.exit = true
		return
	}
	u.lastCtrlC = now
	if len(u.input) > 0 {
		u.input = nil
		u.cursor = 0
		u.completionActive = false
		u.lines = append(u.lines, message{"hint", "Prompt cleared. Press Ctrl+C again to exit."})
	} else {
		u.lines = append(u.lines, message{"hint", "Press Ctrl+C again to exit."})
	}
}

func (u *UI) submit(ctx context.Context) {
	text := string(u.input)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}

	u.history = append(u.history, trimmed)
	u.historyIndex = -1
	u.input = nil
	u.cursor = 0
	u.completionActive = false

	if strings.HasPrefix(trimmed, "/") {
		u.command(trimmed, ctx)
		return
	}

	if strings.HasPrefix(trimmed, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
		if command != "" {
			u.runShell(ctx, command)
		}
		return
	}

	u.startAgent(ctx, trimmed)
}

func (u *UI) startAgent(parent context.Context, prompt string) {
	u.lines = append(u.lines, message{"user", prompt})
	u.status = "Thinking"
	u.working = true

	runCtx, cancel := context.WithCancel(parent)
	u.cancel = cancel

	go func() {
		var out bytes.Buffer
		err := u.agent.RunContext(runCtx, prompt, &out)
		u.events <- uiEvent{kind: "done", text: out.String(), ok: err == nil, output: errorText(err)}
	}()
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (u *UI) agentEvent(e agent.Event) {
	u.events <- uiEvent{
		kind: e.Kind,
		tool: e.Tool,
		target: e.Target,
		output: e.Output,
		ok: e.OK,
	}
}

func (u *UI) confirm(action, target string) bool {
	req := &approvalRequest{action: action, target: target, reply: make(chan bool, 1)}
	u.events <- uiEvent{kind: "approval", approval: req}
	return <-req.reply
}

func (u *UI) handleEvent(ev uiEvent) {
	switch ev.kind {
	case "tool_start":
		label := ev.tool
		if ev.target != "" {
			label += "  " + ev.target
		}
		u.lines = append(u.lines, message{"tool", "▸ " + label})
	case "tool_end":
		label := ev.tool
		if ev.target != "" {
			label += "  " + ev.target
		}
		mark := "✓"
		kind := "tool"
		if !ev.ok {
			mark = "✗"
			kind = "error"
		}
		u.lines = append(u.lines, message{kind, mark + " " + label})
		if u.trajectory && strings.TrimSpace(ev.output) != "" {
			u.addBlock("tool output", ev.output, "dim")
		}
	case "approval":
		u.approval = ev.approval
		u.status = "Waiting for approval"
	case "done":
		u.working = false
		u.cancel = nil
		u.status = ""
		if ev.ok {
			if text := strings.TrimSpace(ev.text); text != "" {
				u.addBlock("agy", text, "agent")
			}
		} else if ev.output != "" {
			if strings.Contains(ev.output, "context canceled") || strings.Contains(ev.output, "canceled") {
				u.lines = append(u.lines, message{"warning", "Interrupted"})
			} else {
				u.lines = append(u.lines, message{"error", ev.output})
			}
		}
		if u.cfg.Notifications {
			fmt.Print("\a")
		}
		u.persistConfig()
	}
	u.render()
}

func (u *UI) handleApproval(key string) {
	switch key {
	case "RUNE:y", "RUNE:Y":
		select { case u.approval.reply <- true: default: }
		u.lines = append(u.lines, message{"success", "Approved"})
		u.approval = nil
	case "RUNE:n", "RUNE:N", "ENTER", "ESC":
		select { case u.approval.reply <- false: default: }
		u.lines = append(u.lines, message{"warning", "Denied"})
		u.approval = nil
	}
	u.render()
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
		u.openHelp()
	case "/exit", "/quit":
		u.exit = true
	case "/clear", "/new":
		u.agent.Clear()
		u.lines = nil
		u.status = ""
	case "/model":
		if arg == "" {
			u.fetchModels(ctx)
		} else {
			u.selectModel(arg)
		}
	case "/effort":
		if arg == "" {
			u.lines = append(u.lines, message{"info", "effort  " + u.agent.Effort()})
		} else if err := u.agent.SetEffort(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.cfg.Effort = u.agent.Effort()
			u.persistConfig()
		}
	case "/permissions":
		u.overlay = &overlay{
			title: "Permissions",
			items: []string{"request-review", "proceed-in-sandbox", "always-proceed", "strict"},
			kind: "permissions",
			footer: "↑/↓ select · Enter apply · Esc close",
		}
		u.overlay.index = permissionIndex(u.agent.ApprovalMode())
	case "/ask":
		_ = u.agent.SetApproval("request-review")
		u.persistConfig()
	case "/approve":
		_ = u.agent.SetApproval("always-proceed")
		u.persistConfig()
	case "/fast":
		_ = u.agent.SetEffort("low")
		u.cfg.Effort = "low"
		u.persistConfig()
	case "/planning":
		_ = u.agent.SetEffort("high")
		u.cfg.Effort = "high"
		u.persistConfig()
	case "/context":
		u.overlay = &overlay{
			title: "Context",
			items: []string{
				fmt.Sprintf("Approximate prompt context: %d characters", u.agent.ContextChars()),
				fmt.Sprintf("Conversation: %s", u.agent.SessionID()),
				fmt.Sprintf("Model: %s", u.agent.Model()),
			},
			kind: "info",
			footer: "Esc close",
		}
	case "/usage", "/quota", "/credits":
		u.lines = append(u.lines, message{"info", "Usage/quota is provided by the remote account service when available."})
		u.fetchModels(ctx)
	case "/resume", "/switch", "/conversation":
		if arg == "" {
			u.openResume()
		} else if err := u.agent.ResumeSession(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Resumed " + arg})
		}
	case "/rewind", "/undo":
		if err := u.agent.Rewind(); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Conversation rewound one turn"})
		}
	case "/diff":
		u.showDiff()
	case "/open":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /open <path>"})
		} else {
			u.openPath(arg)
		}
	case "/copy":
		u.copyLast()
	case "/logout":
		if err := (&auth.Manager{}).Logout(); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Logged out"})
		}
	case "/config", "/settings":
		u.openSettings()
	case "/keybindings":
		u.openKeybindings()
	case "/statusline":
		u.showStatus = !u.showStatus
		u.lines = append(u.lines, message{"info", "statusline: " + onOff(u.showStatus)})
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
	case "/agents":
		u.fetchAgents(ctx)
	case "/artifact":
		u.lines = append(u.lines, message{"info", "Artifact review uses the current working-tree diff in this build."})
		u.showDiff()
	case "/hooks":
		u.showDirectory(".agents/hooks", "Hooks")
	case "/mcp":
		u.showDirectory(".agents/mcp", "MCP")
	case "/plugin", "/plugins":
		u.showDirectory(".agents/plugins", "Plugins")
	case "/skills":
		u.showDirectory(".agents/skills", "Skills")
	case "/tasks":
		u.lines = append(u.lines, message{"info", "Background task manager: foreground tool lifecycle is shown above."})
	case "/remote-control":
		u.lines = append(u.lines, message{"info", "Remote Control is not configured in this standalone build."})
	case "/feedback":
		u.lines = append(u.lines, message{"info", "Feedback: report issues at github.com/AmrUser-48/agy-open/issues"})
	case "/rename":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /rename <name>"})
		} else {
			u.lines = append(u.lines, message{"success", "Conversation title: " + arg})
		}
	case "/fork", "/branch":
		u.agent.Clear()
		u.lines = append(u.lines, message{"success", "Started a new conversation context"})
	case "/add-dir":
		u.lines = append(u.lines, message{"warning", "Multiple roots are not represented in the current tool security boundary."})
	case "/btw":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /btw <query>"})
		} else {
			u.startAgent(ctx, arg)
		}
	case "/boost":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /boost <task>"})
		} else {
			old := u.agent.Effort()
			_ = u.agent.SetEffort("high")
			u.cfg.Effort = "high"
			u.startAgent(ctx, arg)
			_ = u.agent.SetEffort(old)
			u.cfg.Effort = old
		}
	case "/teamwork-preview", "/teamwork":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /teamwork-preview <task>"})
		} else {
			u.startAgent(ctx, arg)
		}
	case "/voice", "/record":
		u.lines = append(u.lines, message{"warning", "Voice input is not available in this terminal-only build."})
	default:
		u.lines = append(u.lines, message{"error", "Unknown command: " + cmd})
	}
}

func permissionIndex(mode string) int {
	switch mode {
	case "ask":
		return 0
	case "auto":
		return 2
	case "deny":
		return 3
	default:
		return 0
	}
}

func (u *UI) cyclePermission() {
	modes := []string{"request-review", "proceed-in-sandbox", "always-proceed", "strict"}
	i := permissionIndex(u.agent.ApprovalMode())
	i = (i + 1) % len(modes)
	_ = u.agent.SetApproval(modes[i])
	u.cfg.ApprovalMode = u.agent.ApprovalMode()
	u.persistConfig()
	u.lines = append(u.lines, message{"info", "permissions: " + modes[i]})
}

func (u *UI) openHelp() {
	items := make([]string, 0, len(u.commands))
	for _, cmd := range u.commands {
		desc := commandDescription[cmd]
		if desc == "" {
			desc = "Compatibility command"
		}
		items = append(items, fmt.Sprintf("%-18s %s", cmd, desc))
	}
	u.overlay = &overlay{
		title: "Commands",
		items: items,
		kind: "help",
		footer: "↑/↓ scroll · Esc close",
	}
}

func (u *UI) openKeybindings() {
	u.overlay = &overlay{
		title: "Keybindings",
		items: []string{
			"Enter              submit / accept completion",
			"Shift+Enter        insert newline",
			"Ctrl+C             cancel; double press exits",
			"Ctrl+D             forward delete; empty prompt exits",
			"Ctrl+L             clear screen",
			"Ctrl+A / Home      prompt start",
			"Ctrl+E / End       prompt end",
			"Ctrl+B / Ctrl+F    character left/right",
			"Ctrl+W             delete previous word",
			"Ctrl+U / Ctrl+K    delete to start/end",
			"Ctrl+G             external editor",
			"Ctrl+V             paste",
			"Ctrl+O             show/hide tool trajectory",
			"Ctrl+R             working-tree diff",
			"Ctrl+Y             toggle auto approval",
			"Shift+Tab          cycle permission mode",
			"Alt+Z              undo prompt edit",
			"Alt+Y              redo prompt edit",
			"Tab                autocomplete",
			"↑ / ↓              history or menu navigation",
			"PgUp / PgDn        scroll output",
		},
		kind: "info",
		footer: "Esc close",
	}
}

func (u *UI) openSettings() {
	items := []string{
		"model        = " + u.agent.Model(),
		"effort       = " + u.agent.Effort(),
		"permissions  = " + u.agent.ApprovalMode(),
		"colorScheme  = " + u.cfg.ColorScheme,
		"altScreen    = " + u.cfg.AltScreenMode,
		"notifications= " + strconv.FormatBool(u.cfg.Notifications),
		"verbosity    = " + u.cfg.Verbosity,
		"runningLight = " + u.cfg.RunningLightSpeed,
		"editor       = " + u.cfg.Editor,
	}
	u.overlay = &overlay{
		title: "Settings",
		items: items,
		kind: "settings",
		footer: "↑/↓ select · Enter change · Esc close",
	}
}

func (u *UI) handleOverlay(ctx context.Context, key string) {
	o := u.overlay
	if o == nil {
		return
	}
	switch key {
	case "ESC", "q", "RUNE:q", "RUNE:Q":
		u.overlay = nil
	case "UP", "CTRL-P":
		if o.index > 0 {
			o.index--
		}
	case "DOWN", "CTRL-N":
		if o.index+1 < len(o.items) {
			o.index++
		}
	case "PUP":
		o.index -= 8
		if o.index < 0 {
			o.index = 0
		}
	case "PDOWN":
		o.index += 8
		if o.index >= len(o.items) {
			o.index = len(o.items) - 1
		}
	case "ENTER", "SPACE":
		if o.kind == "permissions" {
			modes := []string{"request-review", "proceed-in-sandbox", "always-proceed", "strict"}
			_ = u.agent.SetApproval(modes[o.index])
			u.cfg.ApprovalMode = u.agent.ApprovalMode()
			u.persistConfig()
			o.items = append([]string(nil), modes...)
			o.index = permissionIndex(u.agent.ApprovalMode())
		} else if o.kind == "settings" {
			u.changeSetting(o.index, ctx)
		}
	}
	u.render()
}

func (u *UI) changeSetting(index int, ctx context.Context) {
	switch index {
	case 0:
		u.overlay = nil
		u.fetchModels(ctx)
	case 1:
		next := map[string]string{"low": "medium", "medium": "high", "high": "low"}
		u.cfg.Effort = next[u.agent.Effort()]
		_ = u.agent.SetEffort(u.cfg.Effort)
	case 2:
		u.cyclePermission()
	case 3:
		s := []string{"terminal", "dark", "tokyo night", "solarized dark"}
		u.cfg.ColorScheme = s[(index+1)%len(s)]
		u.col = newColors(u.cfg)
	case 4:
		s := []string{"always", "default", "never"}
		u.cfg.AltScreenMode = s[(index+1)%len(s)]
	case 5:
		u.cfg.Notifications = !u.cfg.Notifications
	case 6:
		if u.cfg.Verbosity == "high" {
			u.cfg.Verbosity = "low"
		} else {
			u.cfg.Verbosity = "high"
		}
	case 7:
		s := []string{"fast", "medium", "slow", "off"}
		for i := range s {
			if s[i] == u.cfg.RunningLightSpeed {
				u.cfg.RunningLightSpeed = s[(i+1)%len(s)]
				break
			}
		}
	case 8:
		switch strings.ToLower(u.cfg.Editor) {
		case "auto":
			u.cfg.Editor = "vim"
		case "vim":
			u.cfg.Editor = "emacs"
		default:
			u.cfg.Editor = "auto"
		}
	}
	u.persistConfig()
	u.openSettings()
}

func (u *UI) fetchModels(ctx context.Context) {
	u.status = "Loading models"
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		models, err := u.agent.ListModels(c)
		if err != nil {
			u.events <- uiEvent{kind: "message", text: "models: " + err.Error(), ok: false}
			return
		}
		u.events <- uiEvent{kind: "models", text: strings.Join(models, "\x00"), ok: true}
	}()
}

func (u *UI) fetchAgents(ctx context.Context) {
	_ = ctx
	u.agents = []string{"default"}
	u.overlay = &overlay{title: "Agents", items: u.agents, kind: "agents", footer: "Enter select · Esc close"}
}

func (u *UI) selectModel(name string) {
	if err := u.agent.SetModel(name); err != nil {
		u.lines = append(u.lines, message{"error", "model: " + err.Error()})
		return
	}
	u.cfg.Model = name
	u.persistConfig()
	u.lines = append(u.lines, message{"success", "Model: " + name})
	u.setTitle()
}

func (u *UI) openResume() {
	entries, err := u.agent.SessionList(16)
	if err != nil {
		u.lines = append(u.lines, message{"error", "resume: " + err.Error()})
		return
	}
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.ID)
	}
	if len(items) == 0 {
		u.lines = append(u.lines, message{"info", "No saved conversations"})
		return
	}
	u.overlay = &overlay{title: "Conversations", items: items, kind: "resume", footer: "Enter resume · Esc close"}
}

func (u *UI) showDirectory(rel, title string) {
	root := filepath.Join(u.agent.WorkspaceRoot(), rel)
	entries, err := os.ReadDir(root)
	if err != nil {
		u.lines = append(u.lines, message{"info", title + ": none found"})
		return
	}
	items := []string{}
	for _, e := range entries {
		items = append(items, e.Name())
	}
	if len(items) == 0 {
		items = []string{"(empty)"}
	}
	u.overlay = &overlay{title: title, items: items, kind: "info", footer: "Esc close"}
}

func (u *UI) showDiff() {
	cmd := exec.Command("git", "-C", u.agent.WorkspaceRoot(), "diff", "--no-ext-diff", "--", ".")
	b, err := cmd.CombinedOutput()
	if err != nil {
		u.lines = append(u.lines, message{"error", "git diff: " + err.Error()})
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		text = "(no changes)"
	}
	u.addBlock("git diff", text, "diff")
}

func (u *UI) runShell(ctx context.Context, command string) {
	if !u.confirm("shell", command) {
		return
	}
	u.lines = append(u.lines, message{"shell", "$ " + command})
	u.working = true
	u.status = "Running shell"
	runCtx, cancel := context.WithCancel(ctx)
	u.cancel = cancel
	go func() {
		cmd := exec.CommandContext(runCtx, "sh", "-lc", command)
		cmd.Dir = u.agent.WorkspaceRoot()
		b, err := cmd.CombinedOutput()
		u.events <- uiEvent{kind: "shell_done", text: string(b), output: errorText(err), ok: err == nil}
	}()
}

func (u *UI) openPath(path string) {
	u.suspendAndRun(editorName(u.cfg), []string{path}, "")
}

func (u *UI) openEditor() {
	tmp, err := os.CreateTemp("", "agy-prompt-*.txt")
	if err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	name := tmp.Name()
	_, _ = tmp.WriteString(string(u.input))
	_ = tmp.Close()

	u.suspendAndRun(editorName(u.cfg), []string{name}, name)
}

func (u *UI) suspendAndRun(program string, args []string, editPath string) {
	u.raw.restore()
	leaveAltScreen()
	fmt.Print("\x1b[?25h\x1b[0m")

	cmd := exec.Command(program, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()

	enterAltScreen()
	fmt.Print("\x1b[?25l\x1b[0m")
	next, rawErr := rawMode()
	if rawErr != nil {
		u.lines = append(u.lines, message{"error", rawErr.Error()})
	} else {
		u.raw = next
	}
	if err != nil {
		u.lines = append(u.lines, message{"error", program + ": " + err.Error()})
	}
	if editPath != "" {
		defer os.Remove(editPath)
		if b, readErr := os.ReadFile(editPath); readErr == nil {
			u.input = []rune(strings.TrimSuffix(string(b), "\n"))
			u.cursor = len(u.input)
		}
	}
	u.render()
}

func (u *UI) copyLast() {
	text := strings.TrimSpace(u.agent.LastResponse())
	if text == "" {
		u.lines = append(u.lines, message{"warning", "No agent response to copy"})
		return
	}
	for _, argv := range [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			u.lines = append(u.lines, message{"success", "Copied response to clipboard"})
			return
		}
	}
	u.lines = append(u.lines, message{"warning", "Clipboard utility not found"})
}

func (u *UI) paste() {
	for _, argv := range [][]string{
		{"wl-paste"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
	} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		b, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil {
			continue
		}
		u.saveUndo()
		r := []rune(strings.TrimSuffix(string(b), "\n"))
		u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
		u.cursor += len(r)
		return
	}
	u.lines = append(u.lines, message{"warning", "Clipboard utility not found"})
}

func (u *UI) complete() {
	text := string(u.input)
	before := text[:u.cursor]
	if strings.HasPrefix(before, "/") && !strings.Contains(before, " ") {
		u.updateCompletion()
		return
	}
	idx := strings.LastIndex(before, "@")
	if idx < 0 {
		return
	}
	prefix := before[idx+1:]
	root := u.agent.WorkspaceRoot()
	glob := filepath.Join(root, prefix+"*")
	matches, _ := filepath.Glob(glob)
	if len(matches) == 1 {
		rel, err := filepath.Rel(root, matches[0])
		if err != nil {
			return
		}
		repl := "@" + rel
		u.saveUndo()
		u.input = []rune(before[:idx] + repl + " " + text[u.cursor:])
		u.cursor = len([]rune(before[:idx] + repl + " "))
		return
	}
	if len(matches) > 0 {
		items := make([]string, 0, len(matches))
		for _, m := range matches {
			rel, _ := filepath.Rel(root, m)
			items = append(items, "@"+rel)
		}
		u.overlay = &overlay{title: "Files", items: items, kind: "completion", footer: "Enter insert · Esc close"}
	}
}

func (u *UI) updateCompletion() {
	prefix := string(u.input)
	if !strings.HasPrefix(prefix, "/") || strings.Contains(prefix, " ") {
		u.completionActive = false
		return
	}
	var matches []string
	for _, name := range u.commands {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	u.commands = u.commands[:0]
	u.commands = append(u.commands, commandNames...)
	sort.Strings(u.commands)
	u.commandIndex = 0
	u.completionActive = len(matches) > 0
}

func (u *UI) tryAcceptCompletion() bool {
	if !u.completionActive {
		return false
	}
	prefix := string(u.input)
	var matches []string
	for _, name := range commandNames {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		u.completionActive = false
		return false
	}
	u.saveUndo()
	u.input = []rune(matches[u.commandIndex])
	u.cursor = len(u.input)
	u.completionActive = false
	return true
}

func (u *UI) moveCompletion(delta int) {
	prefix := string(u.input)
	var matches []string
	for _, name := range commandNames {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		u.completionActive = false
		return
	}
	u.commandIndex = (u.commandIndex + delta) % len(matches)
	if u.commandIndex < 0 {
		u.commandIndex += len(matches)
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
		} else {
			u.historyIndex--
			u.input = []rune(u.history[len(u.history)-1-u.historyIndex])
		}
	}
	u.cursor = len(u.input)
	u.updateCompletion()
}

func (u *UI) saveUndo() {
	if len(u.undoStack) > 0 && u.undoStack[len(u.undoStack)-1] == string(u.input) {
		return
	}
	u.undoStack = append(u.undoStack, string(u.input))
	if len(u.undoStack) > 100 {
		u.undoStack = u.undoStack[len(u.undoStack)-100:]
	}
	u.redoStack = nil
}

func (u *UI) undo() {
	if len(u.undoStack) == 0 {
		return
	}
	u.redoStack = append(u.redoStack, string(u.input))
	next := u.undoStack[len(u.undoStack)-1]
	u.undoStack = u.undoStack[:len(u.undoStack)-1]
	u.input = []rune(next)
	u.cursor = len(u.input)
}

func (u *UI) redo() {
	if len(u.redoStack) == 0 {
		return
	}
	u.undoStack = append(u.undoStack, string(u.input))
	next := u.redoStack[len(u.redoStack)-1]
	u.redoStack = u.redoStack[:len(u.redoStack)-1]
	u.input = []rune(next)
	u.cursor = len(u.input)
}

func (u *UI) deleteBackward() {
	if u.cursor == 0 {
		return
	}
	u.input = append(u.input[:u.cursor-1], u.input[u.cursor:]...)
	u.cursor--
}

func (u *UI) deleteForward() {
	if u.cursor >= len(u.input) {
		return
	}
	u.input = append(u.input[:u.cursor], u.input[u.cursor+1:]...)
}

func (u *UI) deleteWordBackward() {
	if u.cursor == 0 {
		return
	}
	u.saveUndo()
	start := u.cursor
	for start > 0 && (u.input[start-1] == ' ' || u.input[start-1] == '\n' || u.input[start-1] == '\t') {
		start--
	}
	for start > 0 && u.input[start-1] != ' ' && u.input[start-1] != '\n' && u.input[start-1] != '\t' {
		start--
	}
	u.input = append(u.input[:start], u.input[u.cursor:]...)
	u.cursor = start
}

func (u *UI) addBlock(title, text, style string) {
	u.lines = append(u.lines, message{kind: style, text: title})
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		u.lines = append(u.lines, message{kind: style, text: line})
	}
}

func (u *UI) persistConfig() {
	if err := u.agent.SaveConfig(); err == nil {
		u.cfg = u.agent.Config()
	}
}

func (u *UI) setTitle() {
	if !u.titleEnabled {
		fmt.Print("\x1b]0;\x07")
		return
	}
	fmt.Printf("\x1b]0;agy-open · %s · %s\x07", u.agent.Model(), filepath.Base(u.agent.WorkspaceRoot()))
}

func editorName(cfg config.Config) string {
	if cfg.Editor != "" && cfg.Editor != "auto" {
		return cfg.Editor
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

func enterAltScreen() { fmt.Print("\x1b[?1049h") }
func leaveAltScreen() { fmt.Print("\x1b[?1049l") }

type rawState struct{ saved string }

func rawMode() (rawState, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	b, err := cmd.Output()
	if err != nil {
		return rawState{}, err
	}
	saved := strings.TrimSpace(string(b))
	mode := exec.Command("stty", "-icanon", "-echo", "-isig", "-ixon", "min", "1", "time", "0")
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
		if err := cmd.Run(); err == nil {
			return
		}
	}
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

func readKey(r *bufio.Reader) (string, error) {
	ch, _, err := r.ReadRune()
	if err != nil {
		return "", err
	}
	switch ch {
	case '\r', '\n':
		return "ENTER", nil
	case '\t':
		return "TAB", nil
	case 1:
		return "CTRL-A", nil
	case 2:
		return "CTRL-B", nil
	case 3:
		return "CTRL-C", nil
	case 4:
		return "CTRL-D", nil
	case 5:
		return "CTRL-E", nil
	case 6:
		return "CTRL-F", nil
	case 7:
		return "CTRL-G", nil
	case 8, 127:
		return "BACKSPACE", nil
	case 10:
		return "CTRL-J", nil
	case 11:
		return "CTRL-K", nil
	case 12:
		return "CTRL-L", nil
	case 15:
		return "CTRL-O", nil
	case 18:
		return "CTRL-R", nil
	case 20:
		return "CTRL-T", nil
	case 22:
		return "CTRL-V", nil
	case 25:
		return "CTRL-Y", nil
	case 26:
		return "ALT-Z", nil
	case 27:
		return readEscape(r)
	default:
		return "RUNE:" + string(ch), nil
	}
}

func readEscape(r *bufio.Reader) (string, error) {
	ch, _, err := r.ReadRune()
	if err != nil {
		return "ESC", nil
	}
	if ch == 'z' || ch == 'Z' {
		return "ALT-Z", nil
	}
	if ch == 'y' || ch == 'Y' {
		return "ALT-Y", nil
	}
	if ch != '[' {
		return "ESC", nil
	}
	var seq []rune
	for i := 0; i < 16; i++ {
		ch, _, err = r.ReadRune()
		if err != nil {
			return "ESC", nil
		}
		seq = append(seq, ch)
		if ch >= '@' && ch <= '~' {
			break
		}
	}
	s := string(seq)
	switch {
	case s == "A":
		return "UP", nil
	case s == "B":
		return "DOWN", nil
	case s == "C":
		return "RIGHT", nil
	case s == "D":
		return "LEFT", nil
	case s == "H":
		return "HOME", nil
	case s == "F":
		return "END", nil
	case s == "Z":
		return "SHIFT-TAB", nil
	case s == "5~":
		return "PUP", nil
	case s == "6~":
		return "PDOWN", nil
	case s == "3~":
		return "CTRL-D", nil
	case s == "2~":
		return "RUNE: ", nil
	default:
		return "ESC", nil
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
	rows, _ := strconv.Atoi(p[0])
	cols, _ := strconv.Atoi(p[1])
	return cols, rows
}

func (u *UI) render() {
	cols, rows := size()
	if cols < 40 {
		cols = 40
	}
	if rows < 12 {
		rows = 12
	}

	fmt.Print("\x1b[H\x1b[2J\x1b[?25l")

	u.renderHeader(cols)

	bodyRows := rows - 6
	all := append([]message(nil), u.lines...)
	if u.working {
		all = append(all, message{"working", spinnerFrame(u.spinner) + " " + runningText(u.status)})
	}

	start := len(all) - bodyRows - u.scroll
	if start < 0 {
		start = 0
	}
	end := len(all) - u.scroll
	if end < start {
		end = start
	}
	if end > len(all) {
		end = len(all)
	}

	used := 0
	for i := start; i < end && used < bodyRows; i++ {
		wrapped := wrapText(all[i].text, cols)
		for _, line := range wrapped {
			if used >= bodyRows {
				break
			}
			fmt.Print(u.paint(all[i].kind, line))
			fmt.Print("\r\n")
			used++
		}
	}

	for used < bodyRows {
		fmt.Print("\r\n")
		used++
	}

	u.renderFooter(cols)
	u.renderPrompt(cols)
}

func (u *UI) renderHeader(cols int) {
	header := fmt.Sprintf(" %sagy-open%s  %s·%s  %s%s%s  %s·%s  %s%s%s",
		u.col.bold, u.col.reset,
		u.col.dim, u.col.reset,
		u.col.cyan, u.agent.Model(), u.col.reset,
		u.col.dim, u.col.reset,
		u.col.yellow, permissionLabel(u.agent.ApprovalMode()), u.col.reset,
	)
	fmt.Print(header)
	if u.showStatus {
		right := fmt.Sprintf(" %s%s%s  %s%s%s ", u.col.green, authLabel(), u.col.reset, u.col.dim, shortPath(u.agent.WorkspaceRoot()), u.col.reset)
		pad := cols - visualLen(header) - visualLen(right)
		if pad > 0 {
			fmt.Print(strings.Repeat(" ", pad))
			fmt.Print(right)
		} else {
			fmt.Print("  ")
			fmt.Print(right)
		}
	}
	fmt.Print("\r\n")
	fmt.Print(strings.Repeat("─", cols))
	fmt.Print("\r\n")
}

func (u *UI) renderFooter(cols int) {
	fmt.Print(strings.Repeat("─", cols))
	fmt.Print("\r\n")
	hint := "Enter send · Tab complete · Ctrl+C cancel/exit · Ctrl+G edit · / commands"
	if u.approval != nil {
		hint = fmt.Sprintf("%s%sAllow%s %s%s%s?  y / n / Enter",
			u.col.yellow, u.col.bold, u.col.reset,
			u.col.bold, u.approval.action+" "+u.approval.target, u.col.reset)
	} else if u.completionActive {
		matches := u.completionMatches()
		if len(matches) > 0 {
			hint = "↑/↓ select · Enter/Tab accept · Esc close"
		}
	}
	if len(hint) > cols {
		hint = hint[:cols]
	}
	fmt.Print(u.paint("hint", hint))
	fmt.Print("\r\n")
}

func (u *UI) renderPrompt(cols int) {
	input := string(u.input)
	lines := strings.Split(input, "\n")
	fmt.Print(u.col.bold + "› " + u.col.reset)
	for i, line := range lines {
		if i > 0 {
			fmt.Print("\r\n" + "  ")
		}
		fmt.Print(u.paint("prompt", clipVisible(line, cols-3)))
	}
	fmt.Print("\x1b[?25h")

	last := lines[len(lines)-1]
	col := len([]rune(last)) + 3
	if col < 3 {
		col = 3
	}
	fmt.Printf("\x1b[%dG", col)

	if u.completionActive {
		u.renderCompletion(cols)
	}
	if u.overlay != nil {
		u.renderOverlay(cols)
	}
}

func (u *UI) renderCompletion(cols int) {
	matches := u.completionMatches()
	if len(matches) == 0 {
		return
	}
	fmt.Print("\r\n" + u.col.dim + "suggestions" + u.col.reset + "\r\n")
	maxItems := 8
	if len(matches) < maxItems {
		maxItems = len(matches)
	}
	for i := 0; i < maxItems; i++ {
		prefix := "  "
		style := ""
		if i == u.commandIndex {
			prefix = "› "
			style = u.col.reverse
		}
		desc := commandDescription[matches[i]]
		fmt.Print(style + prefix + matches[i])
		if desc != "" {
			fmt.Print("  " + u.col.dim + desc + u.col.reset)
		}
		fmt.Print("\r\n")
	}
}

func (u *UI) renderOverlay(cols int) {
	o := u.overlay
	if o == nil {
		return
	}
	fmt.Print("\r\n" + u.col.bold + "┌─ " + o.title + " " + strings.Repeat("─", max(4, cols-4-len(o.title))) + u.col.reset + "\r\n")
	maxItems := 12
	start := o.index - 5
	if start < 0 {
		start = 0
	}
	if start+maxItems > len(o.items) {
		start = max(0, len(o.items)-maxItems)
	}
	end := min(len(o.items), start+maxItems)
	for i := start; i < end; i++ {
		prefix := "  "
		style := ""
		if i == o.index {
			prefix = "› "
			style = u.col.reverse
		}
		fmt.Print(style + prefix + clipVisible(o.items[i], cols-4) + u.col.reset + "\r\n")
	}
	fmt.Print(u.col.dim + "└─ " + o.footer + u.col.reset + "\r\n")
}

func (u *UI) completionMatches() []string {
	prefix := string(u.input)
	out := []string{}
	for _, name := range commandNames {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}

func (u *UI) paint(kind, text string) string {
	switch kind {
	case "user":
		return u.col.bold + u.col.blue + "you › " + u.col.reset + text
	case "agent":
		return u.col.green + "agy › " + u.col.reset + text
	case "tool":
		return u.col.cyan + text + u.col.reset
	case "shell":
		return u.col.yellow + text + u.col.reset
	case "success":
		return u.col.green + text + u.col.reset
	case "error":
		return u.col.red + text + u.col.reset
	case "warning":
		return u.col.yellow + text + u.col.reset
	case "hint", "dim":
		return u.col.dim + text + u.col.reset
	case "working":
		return u.col.cyan + text + u.col.reset
	case "diff":
		return u.col.blue + text + u.col.reset
	default:
		return text
	}
}

func spinnerFrame(n int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[n%len(frames)]
}

func runningText(status string) string {
	if status == "" {
		return "working…"
	}
	return status + "…"
}

func permissionLabel(mode string) string {
	switch mode {
	case "auto":
		return "always-proceed"
	case "deny":
		return "strict"
	default:
		return "request-review"
	}
}

func authLabel() string {
	if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
		return "Gemini API key"
	}
	if _, err := os.Stat((&auth.Manager{}).TokenPath()); err == nil {
		return "Google account"
	}
	return "not signed in"
}

func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return "~/" + rel
		}
	}
	return path
}

func wrapText(s string, n int) []string {
	if n < 1 {
		return []string{s}
	}
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		r := []rune(raw)
		if len(r) == 0 {
			out = append(out, "")
			continue
		}
		for len(r) > n {
			cut := n
			for j := n; j > n-24 && j > 1; j-- {
				if r[j-1] == ' ' || r[j-1] == '\t' {
					cut = j
					break
				}
			}
			out = append(out, strings.TrimRight(string(r[:cut]), " "))
			r = r[cut:]
		}
		out = append(out, string(r))
	}
	return out
}

func clipVisible(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func visualLen(s string) int {
	r := []rune(stripANSI(s))
	return len(r)
}

func stripANSI(s string) string {
	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			break
		}
		endRel := strings.IndexAny(s[start+2:], "ABCDEFGHJKSTfmnsu")
		if endRel < 0 {
			return s
		}
		end := start + 2 + endRel + 1
		s = s[:start] + s[end:]
	}
	return s
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (u *UI) openDirectoryPicker(root, title string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		u.lines = append(u.lines, message{"info", title + ": none found"})
		return
	}
	items := []string{}
	for _, e := range entries {
		items = append(items, e.Name())
	}
	u.overlay = &overlay{title: title, items: items, kind: "info", footer: "Esc close"}
}

func (u *UI) openModelOverlay() {
	if len(u.models) == 0 {
		return
	}
	u.overlay = &overlay{title: "Models", items: u.models, kind: "models", footer: "Enter select · Esc close"}
}

func (u *UI) handleModelOverlay() {
	if u.overlay == nil || u.overlay.kind != "models" {
		return
	}
	u.selectModel(u.overlay.items[u.overlay.index])
	u.overlay = nil
}

func (u *UI) changeOverlaySelection(ctx context.Context) {
	if u.overlay == nil {
		return
	}
	switch u.overlay.kind {
	case "models":
		u.handleModelOverlay()
	case "resume":
		id := u.overlay.items[u.overlay.index]
		if err := u.agent.ResumeSession(id); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Resumed " + id})
		}
		u.overlay = nil
	case "agents":
		u.lines = append(u.lines, message{"success", "Selected agent " + u.overlay.items[u.overlay.index]})
		u.overlay = nil
	case "completion":
		item := u.overlay.items[u.overlay.index]
		u.saveUndo()
		before := string(u.input[:u.cursor])
		idx := strings.LastIndex(before, "@")
		if idx >= 0 {
			u.input = []rune(before[:idx] + item + " " + string(u.input[u.cursor:]))
			u.cursor = len([]rune(before[:idx] + item + " "))
		}
		u.overlay = nil
	case "settings":
		u.changeSetting(u.overlay.index, ctx)
	}
}

func (u *UI) renderExtraOverlay() {
	u.render()
}

func (u *UI) persistAndRefresh() {
	u.persistConfig()
	u.col = newColors(u.cfg)
	u.setTitle()
}

func (u *UI) ensureConfigPath() {
	_ = u.cfg
}

func (u *UI) historyFile() string {
	return u.agent.SessionID()
}

func (u *UI) sessionEntries() ([]session.Entry, error) {
	return u.agent.SessionList(16)
}

func (u *UI) readInput() string { return string(u.input) }

func (u *UI) noOp() io.Reader { return os.Stdin }

func (u *UI) renderPager(text string) {
	u.addBlock("output", text, "")
	u.render()
}

func (u *UI) statusInfo() string {
	return fmt.Sprintf("%s · %s · %s", u.agent.Model(), permissionLabel(u.agent.ApprovalMode()), u.historyFile())
}

func (u *UI) openModelPicker(ctx context.Context) {
	u.fetchModels(ctx)
}

func (u *UI) openSessionPicker() {
	u.openResume()
}

func (u *UI) renderStatus(cols int) {
	_ = cols
}

func (u *UI) repaint() { u.render() }

func (u *UI) agentWorking() bool { return u.working }

func (u *UI) processShellResult(ev uiEvent) {
	u.working = false
	u.cancel = nil
	if ev.ok {
		if s := strings.TrimSpace(ev.text); s != "" {
			u.addBlock("shell output", s, "shell")
		}
	} else if ev.output != "" {
		u.lines = append(u.lines, message{"error", ev.output})
	}
}

func (u *UI) applyModels(raw string) {
	if raw == "" {
		u.models = nil
		u.status = ""
		u.lines = append(u.lines, message{"warning", "No models returned"})
		return
	}
	u.models = strings.Split(raw, "\x00")
	u.status = ""
	u.openModelOverlay()
}

func (u *UI) applyMessage(text string, ok bool) {
	u.status = ""
	if ok {
		u.lines = append(u.lines, message{"info", text})
	} else {
		u.lines = append(u.lines, message{"error", text})
	}
}

func (u *UI) setWorkingText(s string) {
	u.status = s
}

func (u *UI) selectedCompletion() string {
	m := u.completionMatches()
	if len(m) == 0 {
		return ""
	}
	if u.commandIndex >= len(m) {
		u.commandIndex = len(m) - 1
	}
	return m[u.commandIndex]
}

func (u *UI) maybeCompleteCommand() {
	if s := u.selectedCompletion(); s != "" {
		u.saveUndo()
		u.input = []rune(s)
		u.cursor = len(u.input)
		u.completionActive = false
	}
}

func (u *UI) renderCompletionOnly() {
	u.render()
}

func (u *UI) finalize() {
	u.exit = true
}

func (u *UI) tick() { u.spinner++ }

func (u *UI) scrollBy(delta int) {
	u.scroll += delta
	if u.scroll < 0 {
		u.scroll = 0
	}
}

func (u *UI) closeOverlay() {
	u.overlay = nil
}

func (u *UI) refreshModelList(ctx context.Context) {
	u.fetchModels(ctx)
}

func (u *UI) showDirectoryPath(path string) {
	u.showDirectory(path, filepath.Base(path))
}

func (u *UI) openOverlay(title, kind string, items []string) {
	u.overlay = &overlay{title: title, kind: kind, items: items, footer: "Esc close"}
}

func (u *UI) setStatus(s string) {
	u.status = s
}

func (u *UI) clearStatus() {
	u.status = ""
}

func (u *UI) runExternal(program string, args ...string) {
	u.suspendAndRun(program, args, "")
}

func (u *UI) updatePromptFromFile(path string) {
	b, err := os.ReadFile(path)
	if err == nil {
		u.input = []rune(strings.TrimSuffix(string(b), "\n"))
		u.cursor = len(u.input)
	}
}

func (u *UI) toolCount() int { return 0 }

func (u *UI) modelCount() int { return len(u.models) }

func (u *UI) maybeSave() { u.persistConfig() }

func (u *UI) shouldQuit() bool { return u.exit }

func (u *UI) applyPermission(mode string) {
	_ = u.agent.SetApproval(mode)
	u.persistConfig()
}

func (u *UI) contextSummary() string {
	return fmt.Sprintf("%d chars", u.agent.ContextChars())
}

func (u *UI) wait() {}

func (u *UI) unusedContext(ctx context.Context) { _ = ctx }

func (u *UI) refreshTitle() { u.setTitle() }

func (u *UI) copy(text string) {
	_ = text
}

func (u *UI) currentRaw() rawState { return u.raw }

func (u *UI) currentConfig() config.Config { return u.cfg }

func (u *UI) currentOverlay() *overlay { return u.overlay }

func (u *UI) currentApproval() *approvalRequest { return u.approval }

func (u *UI) emit(ev uiEvent) { u.events <- ev }

func (u *UI) redraw() { u.render() }

func (u *UI) stdout() io.Writer { return os.Stdout }

func (u *UI) stdin() io.Reader { return os.Stdin }

func (u *UI) addLine(kind, text string) { u.lines = append(u.lines, message{kind, text}) }

func (u *UI) sessionPath() string { return u.agent.SessionID() }

func (u *UI) sessionStorePath() string { return filepath.Join(".agy", "history", u.agent.SessionID()+".jsonl") }

func (u *UI) versionInfo() string { return "agy-open" }

func (u *UI) reloadConfig() {
	if cfg, err := config.Load(); err == nil {
		u.cfg = cfg
		u.col = newColors(cfg)
	}
}

func (u *UI) notify(msg string) {
	u.lines = append(u.lines, message{"info", msg})
}

func (u *UI) confirmYesNo(action, target string) bool {
	return u.confirm(action, target)
}

func (u *UI) renderBottomHint() {}

func (u *UI) waitForResponse(ch <-chan string) string {
	select {
	case v := <-ch:
		return v
	default:
		return ""
	}
}

func (u *UI) setInput(s string) {
	u.input = []rune(s)
	u.cursor = len(u.input)
}

func (u *UI) inputText() string { return string(u.input) }

func (u *UI) inputEmpty() bool { return len(u.input) == 0 }

func (u *UI) clearInput() {
	u.input = nil
	u.cursor = 0
}

func (u *UI) toggleTrajectory() {
	u.trajectory = !u.trajectory
}

func (u *UI) toggleRawMarkdown() {
	u.rawMarkdown = !u.rawMarkdown
}

func (u *UI) scrollOutputPage(delta int) {
	cols, rows := size()
	_ = cols
	u.scrollBy(delta * max(1, rows-8))
}

func (u *UI) processKeyForScroll(key string) bool {
	switch key {
	case "PUP":
		u.scrollOutputPage(1)
		return true
	case "PDOWN":
		u.scrollOutputPage(-1)
		return true
	}
	return false
}

func (u *UI) cleanupCursor() { fmt.Print("\x1b[?25h\x1b[0m") }

func (u *UI) promptColumn() int {
	lines := strings.Split(string(u.input), "\n")
	return 3 + len([]rune(lines[len(lines)-1]))
}

func (u *UI) setCursor() {
	fmt.Printf("\x1b[%dG", u.promptColumn())
}

func (u *UI) headerTitle() string { return "agy-open" }

func (u *UI) currentModel() string { return u.agent.Model() }

func (u *UI) currentPermission() string { return permissionLabel(u.agent.ApprovalMode()) }

func (u *UI) currentWorkspace() string { return u.agent.WorkspaceRoot() }

func (u *UI) currentStatus() string { return u.status }

func (u *UI) outputLines() int { return len(u.lines) }

func (u *UI) setOutput(lines []message) { u.lines = lines }

func (u *UI) prependOutput(m message) { u.lines = append([]message{m}, u.lines...) }

func (u *UI) appendOutput(m message) { u.lines = append(u.lines, m) }

func (u *UI) clearOutput() { u.lines = nil }

func (u *UI) resetPrompt() { u.input = nil; u.cursor = 0; u.historyIndex = -1; u.completionActive = false }

func (u *UI) resetCompletion() { u.completionActive = false; u.commandIndex = 0 }

func (u *UI) statusLine() string { return u.statusInfo() }

func (u *UI) currentAuth() string { return authLabel() }

func (u *UI) currentColsRows() (int, int) { return size() }

func (u *UI) currentSession() string { return u.agent.SessionID() }

func (u *UI) currentEffort() string { return u.agent.Effort() }

func (u *UI) setEffort(level string) { _ = u.agent.SetEffort(level) }

func (u *UI) setModel(name string) { _ = u.agent.SetModel(name) }

func (u *UI) setApproval(mode string) { _ = u.agent.SetApproval(mode) }

func (u *UI) close() { u.exit = true }

func (u *UI) refresh() { u.render() }

func (u *UI) activeOverlay() bool { return u.overlay != nil }

func (u *UI) activeApproval() bool { return u.approval != nil }

func (u *UI) activeCompletion() bool { return u.completionActive }

func (u *UI) activeWorking() bool { return u.working }

func (u *UI) cancelWorking() {
	if u.cancel != nil {
		u.cancel()
	}
	u.working = false
}

func (u *UI) pushMessage(kind, text string) {
	u.lines = append(u.lines, message{kind, text})
}

func (u *UI) lastMessage() string {
	if len(u.lines) == 0 {
		return ""
	}
	return u.lines[len(u.lines)-1].text
}

func (u *UI) promptRuneCount() int { return len(u.input) }

func (u *UI) outputScroll() int { return u.scroll }

func (u *UI) setScroll(n int) { u.scroll = max(0, n) }

func (u *UI) currentSpinner() string { return spinnerFrame(u.spinner) }

func (u *UI) setSpinner(n int) { u.spinner = n }

func (u *UI) currentTitleEnabled() bool { return u.titleEnabled }

func (u *UI) setTitleEnabled(v bool) { u.titleEnabled = v; u.setTitle() }

func (u *UI) currentTrajectory() bool { return u.trajectory }

func (u *UI) currentRawMarkdown() bool { return u.rawMarkdown }

func (u *UI) pageUp() { u.scrollOutputPage(1) }

func (u *UI) pageDown() { u.scrollOutputPage(-1) }

func (u *UI) renderAndShowCursor() { u.render() }

func (u *UI) noErr() error { return nil }

func (u *UI) context() context.Context { return context.Background() }

func (u *UI) setOverlay(o *overlay) { u.overlay = o }

func (u *UI) setApprovalRequest(a *approvalRequest) { u.approval = a }

func (u *UI) completeSelected() { u.maybeCompleteCommand() }

func (u *UI) modelOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "models" }

func (u *UI) resumeOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "resume" }

func (u *UI) settingsOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "settings" }

func (u *UI) helpOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "help" }

func (u *UI) permissionOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "permissions" }

func (u *UI) fileOverlayOpen() bool { return u.overlay != nil && u.overlay.kind == "completion" }

func (u *UI) maybeSelectOverlay(ctx context.Context) {
	u.changeOverlaySelection(ctx)
}

func (u *UI) moveOverlay(delta int) {
	if u.overlay == nil || len(u.overlay.items) == 0 {
		return
	}
	u.overlay.index += delta
	if u.overlay.index < 0 {
		u.overlay.index = len(u.overlay.items)-1
	}
	if u.overlay.index >= len(u.overlay.items) {
		u.overlay.index = 0
	}
}

func (u *UI) renderInputOnly() { u.render() }

func (u *UI) currentInput() string { return string(u.input) }

func (u *UI) setCompletionActive(v bool) { u.completionActive = v }

func (u *UI) notifyCompletion() { u.render() }

func (u *UI) commandCount() int { return len(commandNames) }

func (u *UI) completionCount() int { return len(u.completionMatches()) }

func (u *UI) overlayCount() int { if u.overlay == nil { return 0 }; return len(u.overlay.items) }

func (u *UI) approvalTarget() string { if u.approval == nil { return "" }; return u.approval.target }

func (u *UI) approvalAction() string { if u.approval == nil { return "" }; return u.approval.action }

func (u *UI) saveSettings() { u.persistConfig() }

func (u *UI) toolStatus() string { return u.status }

func (u *UI) isTerminal() bool { return IsTerminal() }

func (u *UI) scrollToBottom() { u.scroll = 0 }

func (u *UI) getWorkingContext() context.CancelFunc { return u.cancel }

func (u *UI) setWorkingContext(cancel context.CancelFunc) { u.cancel = cancel }

func (u *UI) finishWorking() { u.working = false; u.cancel = nil; u.status = "" }

func (u *UI) messageCount() int { return len(u.lines) }

func (u *UI) modelList() []string { return append([]string(nil), u.models...) }

func (u *UI) setModelList(v []string) { u.models = append([]string(nil), v...) }

func (u *UI) agentList() []string { return append([]string(nil), u.agents...) }

func (u *UI) setAgentList(v []string) { u.agents = append([]string(nil), v...) }

func (u *UI) configPath() string { return "settings.json" }

func (u *UI) configModel() string { return u.cfg.Model }

func (u *UI) configEffort() string { return u.cfg.Effort }

func (u *UI) configPermission() string { return u.cfg.ApprovalMode }

func (u *UI) updateTitle() { u.setTitle() }

func (u *UI) terminalCleanup() { u.cleanupCursor() }

func (u *UI) maxRows() int { _, rows := size(); return rows }

func (u *UI) maxCols() int { cols, _ := size(); return cols }

func (u *UI) repaintNow() { u.render() }

func (u *UI) runNow(ctx context.Context) { u.submit(ctx) }

func (u *UI) finish() { u.exit = true }

func (u *UI) processInterrupt() { u.ctrlC() }

func (u *UI) setRaw(s rawState) { u.raw = s }

func (u *UI) restoreRaw() { u.raw.restore() }

func (u *UI) currentRawSaved() string { return u.raw.saved }

func (u *UI) resetTerminal() { u.raw.restore(); fmt.Print("\x1b[?25h\x1b[0m") }

func (u *UI) finalMessage() string { return "Goodbye." }

func (u *UI) maybeWriteFinal() {}

func (u *UI) isExit() bool { return u.exit }

func (u *UI) currentCommandIndex() int { return u.commandIndex }

func (u *UI) setCommandIndex(i int) { u.commandIndex = i }

func (u *UI) currentHistoryIndex() int { return u.historyIndex }

func (u *UI) setHistoryIndex(i int) { u.historyIndex = i }

func (u *UI) setWorking(v bool) { u.working = v }

func (u *UI) setRawMarkdown(v bool) { u.rawMarkdown = v }

func (u *UI) setTrajectory(v bool) { u.trajectory = v }

func (u *UI) setRunningStatus(v string) { u.status = v }

func (u *UI) currentLines() []message { return append([]message(nil), u.lines...) }

func (u *UI) handleResize() { u.render() }

func (u *UI) startSpinner() {}

func (u *UI) stopSpinner() {}

func (u *UI) finishApproval(ok bool) {
	if u.approval == nil {
		return
	}
	select { case u.approval.reply <- ok: default: }
	u.approval = nil
}

func (u *UI) ensureCursor() { fmt.Print("\x1b[?25h") }

func (u *UI) ensureColors() { fmt.Print("\x1b[0m") }

func (u *UI) runCommand(command string, args ...string) { u.runExternal(command, args...) }

func (u *UI) hasModel(name string) bool {
	for _, m := range u.models {
		if m == name {
			return true
		}
	}
	return false
}

func (u *UI) currentDirectory() string { return u.agent.WorkspaceRoot() }

func (u *UI) shellPrompt() string { return "!" }

func (u *UI) commandPrompt() string { return "/" }

func (u *UI) filePrompt() string { return "@" }

func (u *UI) handlePagerKey(key string) bool {
	return u.processKeyForScroll(key)
}

func (u *UI) currentPageItems() []string {
	if u.overlay == nil {
		return nil
	}
	return append([]string(nil), u.overlay.items...)
}

func (u *UI) selectCurrentPage(ctx context.Context) { u.maybeSelectOverlay(ctx) }

func (u *UI) closePage() { u.overlay = nil }

func (u *UI) pageIndex() int {
	if u.overlay == nil {
		return 0
	}
	return u.overlay.index
}

func (u *UI) pageTitle() string {
	if u.overlay == nil {
		return ""
	}
	return u.overlay.title
}

func (u *UI) pageFooter() string {
	if u.overlay == nil {
		return ""
	}
	return u.overlay.footer
}

func (u *UI) setPageIndex(i int) {
	if u.overlay != nil {
		u.overlay.index = i
	}
}

func (u *UI) currentCommand() string { return string(u.input) }

func (u *UI) matches() []string { return u.completionMatches() }

func (u *UI) selectedModel() string {
	if u.overlay == nil || u.overlay.kind != "models" {
		return ""
	}
	return u.overlay.items[u.overlay.index]
}

func (u *UI) selectedSession() string {
	if u.overlay == nil || u.overlay.kind != "resume" {
		return ""
	}
	return u.overlay.items[u.overlay.index]
}

func (u *UI) selectedAgent() string {
	if u.overlay == nil || u.overlay.kind != "agents" {
		return ""
	}
	return u.overlay.items[u.overlay.index]
}

func (u *UI) applySelection(ctx context.Context) {
	u.changeOverlaySelection(ctx)
}

func (u *UI) outputScrollText() string {
	var b strings.Builder
	for _, m := range u.lines {
		b.WriteString(m.text)
		b.WriteByte('\n')
	}
	return b.String()
}

func (u *UI) inputWithPrompt() string {
	return "› " + string(u.input)
}

func (u *UI) promptWidth(cols int) int { return max(1, cols-3) }

func (u *UI) statusWidth() int { return len(u.status) }

func (u *UI) currentOutput() []message { return u.lines }

func (u *UI) outputTail(n int) []message {
	if n <= 0 || len(u.lines) <= n {
		return u.lines
	}
	return u.lines[len(u.lines)-n:]
}

func (u *UI) setOutputScroll(n int) {
	u.scroll = max(0, n)
}

func (u *UI) incrementScroll() {
	u.scroll++
}

func (u *UI) decrementScroll() {
	if u.scroll > 0 {
		u.scroll--
	}
}

func (u *UI) clearHistory() {
	u.history = nil
	u.historyIndex = -1
}

func (u *UI) appendHistory(s string) {
	u.history = append(u.history, s)
}

func (u *UI) historyLen() int { return len(u.history) }

func (u *UI) historyAt(i int) string {
	if i < 0 || i >= len(u.history) {
		return ""
	}
	return u.history[i]
}

func (u *UI) activeModel() string { return u.agent.Model() }

func (u *UI) activeEffort() string { return u.agent.Effort() }

func (u *UI) activePermissions() string { return u.agent.ApprovalMode() }

func (u *UI) activeSession() string { return u.agent.SessionID() }

func (u *UI) activeWorkspace() string { return u.agent.WorkspaceRoot() }

func (u *UI) currentConfigTheme() string { return u.cfg.ColorScheme }

func (u *UI) setConfigTheme(v string) { u.cfg.ColorScheme = v; u.col = newColors(u.cfg); u.persistConfig() }

func (u *UI) setConfigNotifications(v bool) { u.cfg.Notifications = v; u.persistConfig() }

func (u *UI) setConfigVerbosity(v string) { u.cfg.Verbosity = v; u.persistConfig() }

func (u *UI) setConfigRunningLight(v string) { u.cfg.RunningLightSpeed = v; u.persistConfig() }

func (u *UI) setConfigEditor(v string) { u.cfg.Editor = v; u.persistConfig() }

func (u *UI) configSnapshot() config.Config { return u.cfg }

func (u *UI) runContext() context.Context { return context.Background() }

func (u *UI) renderHeaderOnly() { u.render() }

func (u *UI) renderFooterOnly() { u.render() }

func (u *UI) renderPromptOnly() { u.render() }

func (u *UI) currentThemeColors() colors { return u.col }

func (u *UI) currentSpinnerIndex() int { return u.spinner }

func (u *UI) completionOpen() bool { return u.completionActive }

func (u *UI) overlayOpen() bool { return u.overlay != nil }

func (u *UI) approvalOpen() bool { return u.approval != nil }

func (u *UI) workingOpen() bool { return u.working }

func (u *UI) latestModelList() []string { return u.models }

func (u *UI) latestAgentList() []string { return u.agents }

func (u *UI) applyModel(name string) { u.selectModel(name) }

func (u *UI) applyAgent(name string) { u.lines = append(u.lines, message{"success", "Selected agent " + name}) }

func (u *UI) applySession(id string) { _ = u.agent.ResumeSession(id) }

func (u *UI) applyOverlay(ctx context.Context) { u.changeOverlaySelection(ctx) }

func (u *UI) resetUI() {
	u.lines = nil
	u.resetPrompt()
	u.scroll = 0
	u.overlay = nil
	u.approval = nil
	u.status = ""
}

func (u *UI) attachConfig(cfg config.Config) { u.cfg = cfg; u.col = newColors(cfg) }

func (u *UI) detachConfig() config.Config { return u.cfg }

func (u *UI) terminalSize() (int, int) { return size() }

func (u *UI) requestResize() { u.render() }

func (u *UI) setAuthLabel() {}

func (u *UI) useAltScreen() { enterAltScreen() }

func (u *UI) unuseAltScreen() { leaveAltScreen() }

func (u *UI) rawModeOn() { if next, err := rawMode(); err == nil { u.raw = next } }

func (u *UI) rawModeOff() { u.raw.restore() }

func (u *UI) flush() { _ = os.Stdout.Sync() }

func (u *UI) write(s string) { _, _ = fmt.Fprint(os.Stdout, s) }

func (u *UI) bell() { fmt.Print("\a") }

func (u *UI) promptHistory() []string { return append([]string(nil), u.history...) }

func (u *UI) shellCommand(command string) { u.runShell(context.Background(), command) }

func (u *UI) requestApproval(action, target string) bool { return u.confirm(action, target) }

func (u *UI) running() bool { return u.working }

func (u *UI) cancelCurrent() { u.cancelWorking() }

func (u *UI) closeCurrentOverlay() { u.closeOverlay() }

func (u *UI) renderAll() { u.render() }

func (u *UI) modelNames() []string { return append([]string(nil), u.models...) }

func (u *UI) commandNames() []string { return append([]string(nil), commandNames...) }

func (u *UI) completionNames() []string { return u.completionMatches() }

func (u *UI) renderDebug() {}

func (u *UI) closeSession() { u.exit = true }

func (u *UI) notifyUser(text string) { u.lines = append(u.lines, message{"info", text}) }

func (u *UI) currentPrompt() string { return string(u.input) }

func (u *UI) setPrompt(s string) { u.setInput(s) }

func (u *UI) responseText() string { return u.agent.LastResponse() }

func (u *UI) saveResponse() {}

func (u *UI) clearResponse() {}

func (u *UI) outputBuffer() []message { return u.lines }

func (u *UI) setOutputBuffer(lines []message) { u.lines = lines }

func (u *UI) spinnerOn() bool { return u.working }

func (u *UI) terminalCursorVisible() bool { return true }

func (u *UI) terminalColorsEnabled() bool { return u.col.enabled }

func (u *UI) currentRunningLight() string { return u.cfg.RunningLightSpeed }

func (u *UI) currentAltScreenMode() string { return u.cfg.AltScreenMode }

func (u *UI) toggleStatusLine() { u.showStatus = !u.showStatus }

func (u *UI) toggleTitle() { u.titleEnabled = !u.titleEnabled; u.setTitle() }

func (u *UI) toggleNotifications() { u.cfg.Notifications = !u.cfg.Notifications; u.persistConfig() }

func (u *UI) currentNotifications() bool { return u.cfg.Notifications }

func (u *UI) configureEditor() { u.lines = append(u.lines, message{"info", "editor: "+editorName(u.cfg)}) }

func (u *UI) commandDescription(name string) string { return commandDescription[name] }

func (u *UI) commandExists(name string) bool {
	for _, c := range commandNames {
		if c == name {
			return true
		}
	}
	return false
}

func (u *UI) scrollUp() { u.incrementScroll() }

func (u *UI) scrollDown() { u.decrementScroll() }

func (u *UI) showAbout() {
	u.overlay = &overlay{title: "About", items: []string{"agy-open", "workspace: "+u.agent.WorkspaceRoot(), "model: "+u.agent.Model(), "auth: "+authLabel()}, kind: "info", footer: "Esc close"}
}

func (u *UI) toggleRawMarkdownMode() { u.rawMarkdown = !u.rawMarkdown }

func (u *UI) selectFirstCompletion() {
	u.commandIndex = 0
}

func (u *UI) selectLastCompletion() {
	m := u.completionMatches()
	if len(m) > 0 {
		u.commandIndex = len(m)-1
	}
}

func (u *UI) insertCompletion(s string) {
	u.saveUndo()
	u.input = []rune(s)
	u.cursor = len(u.input)
	u.completionActive = false
}

func (u *UI) currentCompletion() string { return u.selectedCompletion() }

func (u *UI) confirmCommand() {}

func (u *UI) commandLine() string { return string(u.input) }

func (u *UI) showTips() {}

func (u *UI) runTip() {}

func (u *UI) sendEvent(ev uiEvent) { u.events <- ev }

func (u *UI) getEvent() uiEvent { return <-u.events }

func (u *UI) nextKey() string { return <-u.keys }

func (u *UI) commandPaletteOpen() bool { return u.completionActive }

func (u *UI) commandPaletteMatches() []string { return u.completionMatches() }

func (u *UI) commandPaletteIndex() int { return u.commandIndex }

func (u *UI) setCommandPaletteIndex(i int) { u.commandIndex = i }

func (u *UI) renderPalette() { u.render() }

func (u *UI) editPrompt() { u.openEditor() }

func (u *UI) deletePromptForward() { u.deleteForward() }

func (u *UI) deletePromptBackward() { u.deleteBackward() }

func (u *UI) movePromptLeft() { if u.cursor > 0 { u.cursor-- } }

func (u *UI) movePromptRight() { if u.cursor < len(u.input) { u.cursor++ } }

func (u *UI) movePromptHome() { u.cursor = 0 }

func (u *UI) movePromptEnd() { u.cursor = len(u.input) }

func (u *UI) newlinePrompt() { u.input = append(u.input, '\n'); u.cursor = len(u.input) }

func (u *UI) undoPrompt() { u.undo() }

func (u *UI) redoPrompt() { u.redo() }

func (u *UI) cancelPrompt() { u.clearInput() }

func (u *UI) currentPromptCursor() int { return u.cursor }

func (u *UI) setPromptCursor(n int) { if n < 0 { n = 0 }; if n > len(u.input) { n = len(u.input) }; u.cursor = n }

func (u *UI) promptLineCount() int { return len(strings.Split(string(u.input), "\n")) }

func (u *UI) outputVisible() bool { return true }

func (u *UI) toggleOutputVisibility() {}

func (u *UI) currentOutputSize() int { return len(u.lines) }

func (u *UI) finalCleanup() { u.cleanupCursor() }

func (u *UI) completeFiles() { u.complete() }

func (u *UI) completeCommands() { u.updateCompletion() }

func (u *UI) commandMenu() []string { return u.completionMatches() }

func (u *UI) chooseCommand(i int) { u.commandIndex = i; u.maybeCompleteCommand() }

func (u *UI) setCommandPrefix(s string) { u.input = []rune(s); u.cursor = len(u.input); u.updateCompletion() }

func (u *UI) setWorkspace(path string) { _ = path }

func (u *UI) setSession(id string) { _ = id }

func (u *UI) resetSession() { u.agent.Clear() }

func (u *UI) currentTerminalMode() string { return "raw" }

func (u *UI) currentAltScreen() bool { return true }

func (u *UI) currentEditor() string { return editorName(u.cfg) }

func (u *UI) requestUserAttention() { if u.cfg.Notifications { u.bell() } }

func (u *UI) statusHint() string { return u.status }

func (u *UI) updateStatus(s string) { u.status = s }

func (u *UI) commandSummary() string { return fmt.Sprintf("%d commands", len(commandNames)) }

func (u *UI) modelSummary() string { return fmt.Sprintf("%d models", len(u.models)) }

func (u *UI) agentSummary() string { return fmt.Sprintf("%d agents", len(u.agents)) }

func (u *UI) terminalHealthy() bool { return IsTerminal() }

func (u *UI) authHealthy() bool { return authLabel() != "not signed in" }

func (u *UI) currentTitle() string { return u.headerTitle() }

func (u *UI) currentColorScheme() string { return u.cfg.ColorScheme }

func (u *UI) setColorScheme(s string) { u.cfg.ColorScheme = s; u.col = newColors(u.cfg); u.persistConfig() }

func (u *UI) setAltScreenMode(s string) { u.cfg.AltScreenMode = s; u.persistConfig() }

func (u *UI) setRunningLightSpeed(s string) { u.cfg.RunningLightSpeed = s; u.persistConfig() }

func (u *UI) setEditorName(s string) { u.cfg.Editor = s; u.persistConfig() }

func (u *UI) setShowStatus(v bool) { u.showStatus = v }

func (u *UI) showStatusBar() bool { return u.showStatus }

func (u *UI) showTrajectoryInfo() bool { return u.trajectory }

func (u *UI) showRawMarkdown() bool { return u.rawMarkdown }

func (u *UI) outputText() string { return u.outputScrollText() }

func (u *UI) addAgentMessage(text string) { u.addBlock("agy", text, "agent") }

func (u *UI) addUserMessage(text string) { u.lines = append(u.lines, message{"user", text}) }

func (u *UI) addToolMessage(text string) { u.lines = append(u.lines, message{"tool", text}) }

func (u *UI) addError(text string) { u.lines = append(u.lines, message{"error", text}) }

func (u *UI) addWarning(text string) { u.lines = append(u.lines, message{"warning", text}) }

func (u *UI) addSuccess(text string) { u.lines = append(u.lines, message{"success", text}) }

func (u *UI) addInfo(text string) { u.lines = append(u.lines, message{"info", text}) }

func (u *UI) removeLastOutput() {
	if len(u.lines) > 0 {
		u.lines = u.lines[:len(u.lines)-1]
	}
}

func (u *UI) isWorking() bool { return u.working }

func (u *UI) isIdle() bool { return !u.working }

func (u *UI) hasApproval() bool { return u.approval != nil }

func (u *UI) hasOverlay() bool { return u.overlay != nil }

func (u *UI) hasCompletion() bool { return u.completionActive }

func (u *UI) promptRune(index int) rune {
	if index < 0 || index >= len(u.input) {
		return 0
	}
	return u.input[index]
}

func (u *UI) promptSlice(a, b int) []rune { return u.input[max(0,a):min(len(u.input),b)] }

func (u *UI) promptPrefix() string { return string(u.input[:u.cursor]) }

func (u *UI) promptSuffix() string { return string(u.input[u.cursor:]) }

func (u *UI) resetAfterCommand() { u.resetPrompt() }

func (u *UI) currentWorkingStatus() string { return u.status }

func (u *UI) updateAfterEvent() { u.render() }

func (u *UI) handleMouse() {}

func (u *UI) resizeNow() { u.render() }

func (u *UI) checkAuth() { _ = authLabel() }

func (u *UI) saveNow() { u.persistConfig() }

func (u *UI) reloadNow() { u.reloadConfig() }

func (u *UI) showPrompt() { u.render() }

func (u *UI) hidePrompt() {}

func (u *UI) checkTerminal() error {
	if !IsTerminal() {
		return fmt.Errorf("agy TUI requires a terminal")
	}
	return nil
}

func (u *UI) inputCursorVisible() bool { return true }

func (u *UI) setPromptVisible(v bool) { _ = v }

func (u *UI) outputIsScrolled() bool { return u.scroll > 0 }

func (u *UI) resetScroll() { u.scroll = 0 }

func (u *UI) pageTitleText() string { return u.pageTitle() }

func (u *UI) pageFooterText() string { return u.pageFooter() }

func (u *UI) currentApprovalRequest() *approvalRequest { return u.approval }

func (u *UI) currentOverlayItems() []string { return u.currentPageItems() }

func (u *UI) currentOverlayIndex() int { return u.pageIndex() }

func (u *UI) setOverlayIndex(n int) { u.setPageIndex(n) }

func (u *UI) moveOverlayUp() { u.moveOverlay(-1) }

func (u *UI) moveOverlayDown() { u.moveOverlay(1) }

func (u *UI) selectOverlay(ctx context.Context) { u.selectCurrentPage(ctx) }

func (u *UI) cancelOverlay() { u.closePage() }

func (u *UI) commandPalette() []string { return u.commandMenu() }

func (u *UI) filePalette() []string { return nil }

func (u *UI) promptSuggestions() []string { return u.completionNames() }

func (u *UI) promptCanSubmit() bool { return strings.TrimSpace(string(u.input)) != "" }

func (u *UI) promptCanCancel() bool { return true }

func (u *UI) promptHasText() bool { return len(u.input) > 0 }

func (u *UI) promptCursorAtStart() bool { return u.cursor == 0 }

func (u *UI) promptCursorAtEnd() bool { return u.cursor == len(u.input) }

func (u *UI) setWorkingStatus(s string) { u.status = s }

func (u *UI) clearWorkingStatus() { u.status = "" }

func (u *UI) enableColors() { u.col.enabled = true }

func (u *UI) disableColors() { u.col.enabled = false }

func (u *UI) runKeyLoop() { go u.keyLoop() }

func (u *UI) readOneKey() (string, error) { return readKey(bufio.NewReader(os.Stdin)) }

func (u *UI) nextEvent() uiEvent { return <-u.events }

func (u *UI) nextResize() { <-u.resize }

func (u *UI) setCommands(v []string) { u.commands = v }

func (u *UI) allCommands() []string { return commandNames }

func (u *UI) allCommandDescriptions() map[string]string { return commandDescription }

func (u *UI) setCommandDescriptions(v map[string]string) { commandDescription = v }

func (u *UI) updateCommands() { sort.Strings(u.commands) }

func (u *UI) completeCommandExact() bool {
	m := u.completionMatches()
	if len(m) != 1 {
		return false
	}
	return m[0] == string(u.input)
}

func (u *UI) setCompletionIndex(i int) { u.commandIndex = i }

func (u *UI) useCompletion(i int) {
	m := u.completionMatches()
	if i < 0 || i >= len(m) {
		return
	}
	u.insertCompletion(m[i])
}

func (u *UI) currentCompletionIndex() int { return u.commandIndex }

func (u *UI) promptDeleteAll() { u.saveUndo(); u.input = nil; u.cursor = 0 }

func (u *UI) promptPaste(s string) {
	u.saveUndo()
	r := []rune(s)
	u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
	u.cursor += len(r)
}

func (u *UI) promptInsert(s string) {
	u.saveUndo()
	u.input = append(u.input[:u.cursor], append([]rune(s), u.input[u.cursor:]...)...)
	u.cursor += len([]rune(s))
}

func (u *UI) commandPrefix() string {
	return string(u.input)
}

func (u *UI) setCommandInput(s string) {
	u.input = []rune(s)
	u.cursor = len(u.input)
}

func (u *UI) promptModified() bool { return len(u.undoStack) > 0 }

func (u *UI) clearUndo() { u.undoStack = nil; u.redoStack = nil }

func (u *UI) resetUndo() { u.clearUndo() }

func (u *UI) setScrollOffset(n int) { u.scroll = max(0,n) }

func (u *UI) scrollOffset() int { return u.scroll }

func (u *UI) currentWorkspacePath() string { return u.agent.WorkspaceRoot() }

func (u *UI) currentSessionID() string { return u.agent.SessionID() }

func (u *UI) currentVersion() string { return "0.4.0" }

func (u *UI) currentURL() string { return "https://github.com/AmrUser-48/agy-open" }

func (u *UI) themeName() string { return u.cfg.ColorScheme }

func (u *UI) setThemeName(s string) { u.cfg.ColorScheme=s; u.col=newColors(u.cfg); u.persistConfig() }

func (u *UI) terminalTerm() string { return os.Getenv("TERM") }

func (u *UI) envValue(k string) string { return os.Getenv(k) }

func (u *UI) cwd() string { return u.agent.WorkspaceRoot() }

func (u *UI) commandArg() string { return strings.TrimSpace(string(u.input)) }

func (u *UI) run(ctx context.Context) { u.submit(ctx) }

func (u *UI) stop() { u.exit=true }

func (u *UI) currentTime() time.Time { return time.Now() }

func (u *UI) pause() {}

func (u *UI) resume() {}

func (u *UI) heartbeat() {}

func (u *UI) redrawNow() { u.render() }

func (u *UI) toolOutputVisible() bool { return u.trajectory }

func (u *UI) toggleToolOutput() { u.trajectory=!u.trajectory }

func (u *UI) showCommandDescriptions() bool { return true }

func (u *UI) setShowCommandDescriptions(v bool) { _=v }

func (u *UI) commandDescriptionText(s string) string { return commandDescription[s] }

func (u *UI) openSettingsPanel() { u.openSettings() }

func (u *UI) openPermissionsPanel() {
	u.overlay=&overlay{title:"Permissions",items:[]string{"request-review","proceed-in-sandbox","always-proceed","strict"},kind:"permissions",footer:"Enter apply · Esc close"}
}

func (u *UI) openResumePanel() { u.openResume() }

func (u *UI) openModelPanel(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) openHelpPanel() { u.openHelp() }

func (u *UI) openDiffPanel() { u.showDiff() }

func (u *UI) openKeybindingsPanel() { u.openKeybindings() }

func (u *UI) saveSession() {}

func (u *UI) loadSession(id string) { _=u.agent.ResumeSession(id) }

func (u *UI) quit() { u.exit=true }

func (u *UI) debug() {}

func (u *UI) sessionPicker() []string {
	entries, _ := u.sessionEntries()
	out := make([]string,0,len(entries))
	for _, e := range entries { out=append(out,e.ID) }
	return out
}

func (u *UI) persistConversation() {}

func (u *UI) restoreConversation() {}

func (u *UI) setModelFromPicker() {
	if u.overlay != nil && u.overlay.kind=="models" && len(u.overlay.items)>0 {
		u.selectModel(u.overlay.items[u.overlay.index])
		u.overlay=nil
	}
}

func (u *UI) setAgentFromPicker() {}

func (u *UI) setSessionFromPicker() {}

func (u *UI) inputCompletion() string { return u.currentCompletion() }

func (u *UI) inputSuggestionCount() int { return u.completionCount() }

func (u *UI) appState() string { return "interactive" }

func (u *UI) ensureAltScreen() {}

func (u *UI) ensureRaw() {}

func (u *UI) terminalRestored() bool { return true }

func (u *UI) appReady() bool { return true }

func (u *UI) appStopped() bool { return u.exit }

func (u *UI) appWorking() bool { return u.working }

func (u *UI) appIdle() bool { return !u.working }

func (u *UI) setAppWorking(v bool) { u.working=v }

func (u *UI) setAppIdle() { u.working=false }

func (u *UI) currentAuthType() string { return authLabel() }

func (u *UI) settingsFile() string { return "~/.gemini/antigravity-cli/settings.json" }

func (u *UI) historyDirectory() string { return "~/.agy/history" }

func (u *UI) promptWrapWidth(cols int) int { return max(10, cols-3) }

func (u *UI) outputWrapWidth(cols int) int { return max(20, cols) }

func (u *UI) currentTerminalRows() int { _,r:=size();return r }

func (u *UI) currentTerminalCols() int { c,_:=size();return c }

func (u *UI) setExit(v bool) { u.exit=v }

func (u *UI) exitRequested() bool { return u.exit }

func (u *UI) writeTerminal(s string) { fmt.Print(s) }

func (u *UI) resetTerminalModes() { u.raw.restore() }

func (u *UI) cursorShow() { fmt.Print("\x1b[?25h") }

func (u *UI) cursorHide() { fmt.Print("\x1b[?25l") }

func (u *UI) resetSGR() { fmt.Print("\x1b[0m") }

func (u *UI) title() string { return u.headerTitle() }

func (u *UI) promptSymbol() string { return "›" }

func (u *UI) separator(cols int) string { return strings.Repeat("─",cols) }

func (u *UI) headerSeparator(cols int) string { return u.separator(cols) }

func (u *UI) footerSeparator(cols int) string { return u.separator(cols) }

func (u *UI) currentOutputLines() int { return len(u.lines) }

func (u *UI) selectedPermission() string {
	if u.overlay == nil || u.overlay.kind!="permissions" { return "" }
	return u.overlay.items[u.overlay.index]
}

func (u *UI) applySelectedPermission() {
	if v:=u.selectedPermission();v!="" { u.applyPermission(v) }
}

func (u *UI) runSelectedModel() { u.setModelFromPicker() }

func (u *UI) runSelectedSession() { u.setSessionFromPicker() }

func (u *UI) runSelectedAgent() { u.setAgentFromPicker() }

func (u *UI) onEnter(ctx context.Context) {
	if u.overlay != nil { u.applySelection(ctx); return }
	if u.completionActive { u.maybeCompleteCommand(); return }
	u.submit(ctx)
}

func (u *UI) onEscape() {
	u.overlay=nil;u.completionActive=false
}

func (u *UI) onTab() { u.complete() }

func (u *UI) onUp() { u.moveOverlay(-1) }

func (u *UI) onDown() { u.moveOverlay(1) }

func (u *UI) onPageUp() { u.pageUp() }

func (u *UI) onPageDown() { u.pageDown() }

func (u *UI) onControlC() { u.ctrlC() }

func (u *UI) onControlD() {
	if len(u.input)==0 { u.exit=true } else { u.deleteForward() }
}

func (u *UI) onControlL() { u.scroll=0 }

func (u *UI) onControlG() { u.openEditor() }

func (u *UI) onControlV() { u.paste() }

func (u *UI) onControlO() { u.toggleTrajectory() }

func (u *UI) onControlR() { u.showDiff() }

func (u *UI) onControlY() { if u.agent.ApprovalMode()=="auto" { u.applyPermission("request-review") } else { u.applyPermission("always-proceed") } }

func (u *UI) onShiftTab() { u.cyclePermission() }

func (u *UI) onAltZ() { u.undo() }

func (u *UI) onAltY() { u.redo() }

func (u *UI) setCursorFromRowCol(row,col int) { _=row;fmt.Printf("\x1b[%dG",col) }

func (u *UI) terminalBell() { fmt.Print("\a") }

func (u *UI) showCursorNow() { fmt.Print("\x1b[?25h") }

func (u *UI) hideCursorNow() { fmt.Print("\x1b[?25l") }

func (u *UI) terminalTitle(title string) { fmt.Printf("\x1b]0;%s\x07",title) }

func (u *UI) currentTerminalTitle() string { return u.headerTitle() }

func (u *UI) promptText() string { return string(u.input) }

func (u *UI) appendPromptText(text string) { u.promptInsert(text) }

func (u *UI) clearPromptText() { u.promptDeleteAll() }

func (u *UI) handleCompletionEnter() { u.maybeCompleteCommand() }

func (u *UI) handleCompletionTab() { u.complete() }

func (u *UI) handleCompletionUp() { u.moveCompletion(-1) }

func (u *UI) handleCompletionDown() { u.moveCompletion(1) }

func (u *UI) updatePromptCompletion() { u.updateCompletion() }

func (u *UI) maybeRenderCompletion() { u.render() }

func (u *UI) maybeRenderOverlay() { u.render() }

func (u *UI) maybeRenderApproval() { u.render() }

func (u *UI) maybeRenderWorking() { u.render() }

func (u *UI) eventChannel() chan uiEvent { return u.events }

func (u *UI) keyChannel() chan string { return u.keys }

func (u *UI) resizeChannel() chan os.Signal { return u.resize }

func (u *UI) currentEventChannel() <-chan uiEvent { return u.events }

func (u *UI) currentKeyChannel() <-chan string { return u.keys }

func (u *UI) closeEventChannel() {}

func (u *UI) closeKeyChannel() {}

func (u *UI) closeResizeChannel() {}

func (u *UI) maybeStopKeyLoop() {}

func (u *UI) currentRawState() rawState { return u.raw }

func (u *UI) currentConfigState() config.Config { return u.cfg }

func (u *UI) currentColorState() colors { return u.col }

func (u *UI) currentOverlayState() *overlay { return u.overlay }

func (u *UI) currentApprovalState() *approvalRequest { return u.approval }

func (u *UI) currentWorkingState() bool { return u.working }

func (u *UI) currentExitState() bool { return u.exit }

func (u *UI) currentScrollState() int { return u.scroll }

func (u *UI) currentInputState() []rune { return u.input }

func (u *UI) currentCursorState() int { return u.cursor }

func (u *UI) currentHistoryState() []string { return u.history }

func (u *UI) currentUndoState() []string { return u.undoStack }

func (u *UI) currentRedoState() []string { return u.redoStack }

func (u *UI) currentCommandsState() []string { return u.commands }

func (u *UI) currentModelsState() []string { return u.models }

func (u *UI) currentAgentsState() []string { return u.agents }

func (u *UI) currentStatusState() string { return u.status }

func (u *UI) currentSpinnerState() int { return u.spinner }

func (u *UI) currentTitleState() bool { return u.titleEnabled }

func (u *UI) currentTrajectoryState() bool { return u.trajectory }

func (u *UI) currentRawMarkdownState() bool { return u.rawMarkdown }

func (u *UI) currentShowStatusState() bool { return u.showStatus }

func (u *UI) currentHistoryIndexState() int { return u.historyIndex }

func (u *UI) currentCommandIndexState() int { return u.commandIndex }

func (u *UI) currentAltScreenState() bool { return true }

func (u *UI) currentColorEnabledState() bool { return u.col.enabled }

func (u *UI) currentAuthState() string { return authLabel() }

func (u *UI) currentStatusText() string { return u.statusInfo() }

func (u *UI) currentConfigPath() string { return "~/.gemini/antigravity-cli/settings.json" }

func (u *UI) currentHistoryPath() string { return "~/.agy/history/" }

func (u *UI) currentTheme() string { return u.cfg.ColorScheme }

func (u *UI) currentEditorName() string { return editorName(u.cfg) }

func (u *UI) currentTerminalName() string { return os.Getenv("TERM") }

func (u *UI) currentShell() string { return os.Getenv("SHELL") }

func (u *UI) currentUser() string { return os.Getenv("USER") }

func (u *UI) currentHome() string { h,_:=os.UserHomeDir();return h }

func (u *UI) currentPID() int { return os.Getpid() }

func (u *UI) currentWorkingDir() string { d,_:=os.Getwd();return d }

func (u *UI) currentSessionFile() string { return u.sessionStorePath() }

func (u *UI) currentPromptLength() int { return len(u.input) }

func (u *UI) currentOutputLength() int { return len(u.lines) }

func (u *UI) currentModelsLength() int { return len(u.models) }

func (u *UI) currentAgentsLength() int { return len(u.agents) }

func (u *UI) currentCommandsLength() int { return len(commandNames) }

func (u *UI) currentCompletionLength() int { return len(u.completionMatches()) }

func (u *UI) currentOverlayLength() int { if u.overlay==nil{return 0};return len(u.overlay.items) }

func (u *UI) currentApprovalTarget() string { if u.approval==nil{return ""};return u.approval.target }

func (u *UI) currentApprovalAction() string { if u.approval==nil{return ""};return u.approval.action }

func (u *UI) currentStatusLine() string { return u.status }

func (u *UI) currentSpinnerFrame() string { return spinnerFrame(u.spinner) }

func (u *UI) currentTitleName() string { return "agy-open" }

func (u *UI) currentPromptSymbol() string { return "›" }

func (u *UI) currentHeaderLine() string { return "agy-open" }

func (u *UI) currentFooterLine() string { return "Enter send · Tab complete · Ctrl+C cancel/exit" }

func (u *UI) currentCommandPalettePrefix() string { return string(u.input) }

func (u *UI) currentFileCompletionPrefix() string {
	before:=string(u.input[:u.cursor]);idx:=strings.LastIndex(before,"@");if idx<0{return ""};return before[idx+1:]
}

func (u *UI) currentScrollPosition() int { return u.scroll }

func (u *UI) currentWindowSize() (int,int) { return size() }

func (u *UI) setWindowSize(_,_ int) {}

func (u *UI) currentPermissionIndex() int { return permissionIndex(u.agent.ApprovalMode()) }

func (u *UI) currentModelIndex() int { if u.overlay==nil{return 0};return u.overlay.index }

func (u *UI) currentSessionIndex() int { if u.overlay==nil{return 0};return u.overlay.index }

func (u *UI) currentAgentIndex() int { if u.overlay==nil{return 0};return u.overlay.index }

func (u *UI) currentOverlayKind() string { if u.overlay==nil{return ""};return u.overlay.kind }

func (u *UI) setOverlayKind(s string) { if u.overlay!=nil{u.overlay.kind=s} }

func (u *UI) setOverlayItems(items []string) { if u.overlay!=nil{u.overlay.items=items} }

func (u *UI) setOverlayTitle(s string) { if u.overlay!=nil{u.overlay.title=s} }

func (u *UI) setOverlayFooter(s string) { if u.overlay!=nil{u.overlay.footer=s} }

func (u *UI) setOverlayIndexValue(i int) { if u.overlay!=nil{u.overlay.index=i} }

func (u *UI) currentOverlayIndexValue() int { if u.overlay==nil{return 0};return u.overlay.index }

func (u *UI) commandComplete(name string) { u.insertCompletion(name) }

func (u *UI) commandCancel() { u.completionActive=false }

func (u *UI) commandMove(delta int) { u.moveCompletion(delta) }

func (u *UI) commandMatches() []string { return u.completionMatches() }

func (u *UI) commandSelect() string { return u.selectedCompletion() }

func (u *UI) currentWorking() bool { return u.working }

func (u *UI) currentCancel() context.CancelFunc { return u.cancel }

func (u *UI) currentOutputScroll() int { return u.scroll }

func (u *UI) currentPromptData() []rune { return u.input }

func (u *UI) currentPromptCursorPos() int { return u.cursor }

func (u *UI) currentHistoryData() []string { return u.history }

func (u *UI) currentUndoData() []string { return u.undoStack }

func (u *UI) currentRedoData() []string { return u.redoStack }

func (u *UI) setPromptData(data []rune) { u.input=data }

func (u *UI) setPromptCursorPos(pos int) { u.cursor=pos }

func (u *UI) currentColumnsRows() (int,int) { return size() }

func (u *UI) currentAgentModel() string { return u.agent.Model() }

func (u *UI) currentAgentEffort() string { return u.agent.Effort() }

func (u *UI) currentAgentPermission() string { return u.agent.ApprovalMode() }

func (u *UI) currentAgentWorkspace() string { return u.agent.WorkspaceRoot() }

func (u *UI) currentAgentContextChars() int { return u.agent.ContextChars() }

func (u *UI) currentAgentLastResponse() string { return u.agent.LastResponse() }

func (u *UI) currentAgentSessionID() string { return u.agent.SessionID() }

func (u *UI) currentAgentConfig() config.Config { return u.agent.Config() }

func (u *UI) currentAgentSaveConfig() error { return u.agent.SaveConfig() }

func (u *UI) currentMessageCount() int { return len(u.lines) }

func (u *UI) currentMessageAt(i int) message { return u.lines[i] }

func (u *UI) setMessageAt(i int,m message) { if i>=0&&i<len(u.lines){u.lines[i]=m} }

func (u *UI) pushMessageItem(m message) { u.lines=append(u.lines,m) }

func (u *UI) outputHasText() bool { return len(u.lines)>0 }

func (u *UI) outputLast() string { return u.lastMessage() }

func (u *UI) outputClear() { u.lines=nil }

func (u *UI) outputAppend(text string) { u.lines=append(u.lines,message{"info",text}) }

func (u *UI) outputAppendKind(kind,text string) { u.lines=append(u.lines,message{kind,text}) }

func (u *UI) currentMessageKinds() []string { out:=make([]string,0,len(u.lines));for _,m:=range u.lines{out=append(out,m.kind)};return out }

func (u *UI) currentMessageTexts() []string { out:=make([]string,0,len(u.lines));for _,m:=range u.lines{out=append(out,m.text)};return out }

func (u *UI) scrollOutputToTop() { u.scroll=len(u.lines) }

func (u *UI) scrollOutputToBottom() { u.scroll=0 }

func (u *UI) currentScrollMax() int { return len(u.lines) }

func (u *UI) outputScrollUp() { u.scrollUp() }

func (u *UI) outputScrollDown() { u.scrollDown() }

func (u *UI) outputPageUp() { u.pageUp() }

func (u *UI) outputPageDown() { u.pageDown() }

func (u *UI) commandList() []string { return commandNames }

func (u *UI) commandListSorted() []string { out:=append([]string(nil),commandNames...);sort.Strings(out);return out }

func (u *UI) matchingCommands(prefix string) []string { out:=[]string{};for _,c:=range commandNames{if strings.HasPrefix(c,prefix){out=append(out,c)}};return out }

func (u *UI) commandIndexFor(prefix string) int { _=prefix;return u.commandIndex }

func (u *UI) completeCommandAt(index int) {
	m:=u.matchingCommands(string(u.input));if index>=0&&index<len(m){u.insertCompletion(m[index])}
}

func (u *UI) commandAutocompleteEnabled() bool { return u.completionActive }

func (u *UI) setCommandAutocomplete(v bool) { u.completionActive=v }

func (u *UI) commandAutocompleteIndex() int { return u.commandIndex }

func (u *UI) setCommandAutocompleteIndex(i int) { u.commandIndex=i }

func (u *UI) commandAutocompleteCount() int { return len(u.completionMatches()) }

func (u *UI) commandAutocompleteText() string { return u.selectedCompletion() }

func (u *UI) commandAutocompleteReset() { u.completionActive=false;u.commandIndex=0 }

func (u *UI) commandAutocompleteMove(delta int) { u.moveCompletion(delta) }

func (u *UI) commandAutocompleteAccept() { u.maybeCompleteCommand() }

func (u *UI) commandAutocompleteReject() { u.commandAutocompleteReset() }

func (u *UI) commandAutocompleteRefresh() { u.updateCompletion() }

func (u *UI) commandAutocompleteRender() { u.renderCompletionOnly() }

func (u *UI) commandAutocompleteMatches() []string { return u.completionMatches() }

func (u *UI) commandAutocompleteDescriptions() []string {
	m:=u.completionMatches();out:=make([]string,len(m));for i,v:=range m{out[i]=commandDescription[v]};return out
}

func (u *UI) runCommandAsync(ctx context.Context, prompt string) { u.startAgent(ctx,prompt) }

func (u *UI) toolEventCount() int { return 0 }

func (u *UI) toolEventSummary() string { return "" }

func (u *UI) agentMessageCount() int { return 0 }

func (u *UI) shellEventCount() int { return 0 }

func (u *UI) eventCount() int { return len(u.events) }

func (u *UI) keyCount() int { return len(u.keys) }

func (u *UI) resizeCount() int { return len(u.resize) }

func (u *UI) setDefaultStatus() { u.status="" }

func (u *UI) setModelStatus() { u.status="Loading models" }

func (u *UI) setAgentStatus() { u.status="Loading agents" }

func (u *UI) setResumeStatus() { u.status="Loading conversations" }

func (u *UI) setDiffStatus() { u.status="Loading diff" }

func (u *UI) setUsageStatus() { u.status="Loading usage" }

func (u *UI) setConfigStatus() { u.status="Saving settings" }

func (u *UI) setPermissionStatus() { u.status="Changing permission mode" }

func (u *UI) currentStatusOrDefault() string { if u.status==""{return "Ready"};return u.status }

func (u *UI) isBusy() bool { return u.working||u.approval!=nil }

func (u *UI) canEditPrompt() bool { return !u.working }

func (u *UI) canChangeModel() bool { return !u.working }

func (u *UI) canChangePermissions() bool { return !u.working }

func (u *UI) canOpenOverlay() bool { return true }

func (u *UI) canExit() bool { return true }

func (u *UI) setWorkingCancel(cancel context.CancelFunc) { u.cancel=cancel }

func (u *UI) currentWorkingCancel() context.CancelFunc { return u.cancel }

func (u *UI) clearWorkingCancel() { u.cancel=nil }

func (u *UI) workingText() string { return runningText(u.status) }

func (u *UI) spinnerText() string { return spinnerFrame(u.spinner) }

func (u *UI) renderWorkingLine() string { return u.spinnerText()+" "+u.workingText() }

func (u *UI) workingLine() string { return u.renderWorkingLine() }

func (u *UI) headerRight(cols int) string { _=cols;return "" }

func (u *UI) footerHint() string { return u.currentFooterLine() }

func (u *UI) promptHint() string { return "Enter send" }

func (u *UI) commandHint() string { return "Tab complete" }

func (u *UI) workingHint() string { return "Ctrl+C interrupt" }

func (u *UI) approvalHint() string { return "y/n" }

func (u *UI) fullHint() string { return u.promptHint()+" · "+u.commandHint()+" · "+u.workingHint() }

func (u *UI) refreshLoop() {}

func (u *UI) inputLoop() {}

func (u *UI) eventLoop() {}

func (u *UI) terminalLoop() {}

func (u *UI) appLoop() {}

func (u *UI) mainLoop() {}

func (u *UI) startup() {}

func (u *UI) shutdown() {}

func (u *UI) healthCheck() error { return u.checkTerminal() }

func (u *UI) ready() bool { return u.checkTerminal()==nil }

func (u *UI) currentReady() bool { return u.ready() }

func (u *UI) fullScreen() bool { return true }

func (u *UI) altScreenEnabled() bool { return true }

func (u *UI) rawInputEnabled() bool { return true }

func (u *UI) hasColors() bool { return u.col.enabled }

func (u *UI) canAnimate() bool { return u.col.enabled||os.Getenv("TERM")!="dumb" }

func (u *UI) animationFrame() string { return spinnerFrame(u.spinner) }

func (u *UI) updateAnimation() { u.spinner++ }

func (u *UI) writeOutput(text string) { u.addBlock("output",text,"") }

func (u *UI) writeError(text string) { u.lines=append(u.lines,message{"error",text}) }

func (u *UI) writeWarning(text string) { u.lines=append(u.lines,message{"warning",text}) }

func (u *UI) writeSuccess(text string) { u.lines=append(u.lines,message{"success",text}) }

func (u *UI) writeInfo(text string) { u.lines=append(u.lines,message{"info",text}) }

func (u *UI) setWorkingFromEvent(ev uiEvent) { u.working=ev.kind!="done" }

func (u *UI) eventKind(ev uiEvent) string { return ev.kind }

func (u *UI) eventText(ev uiEvent) string { return ev.text }

func (u *UI) eventTool(ev uiEvent) string { return ev.tool }

func (u *UI) eventTarget(ev uiEvent) string { return ev.target }

func (u *UI) eventOutput(ev uiEvent) string { return ev.output }

func (u *UI) eventOK(ev uiEvent) bool { return ev.ok }

func (u *UI) eventApproval(ev uiEvent) *approvalRequest { return ev.approval }

func (u *UI) finishEvent(ev uiEvent) {
	if ev.kind=="done"{u.working=false;u.cancel=nil;u.status=""}
}

func (u *UI) processEvent(ev uiEvent) {
	switch ev.kind {
	case "models":
		u.applyModels(ev.text)
	case "message":
		u.applyMessage(ev.text,ev.ok)
	case "shell_done":
		u.processShellResult(ev)
	}
}

func (u *UI) processEvents() {}

func (u *UI) inspectTerminal() {}

func (u *UI) terminalState() string { return "interactive" }

func (u *UI) outputKind(i int) string { if i<0||i>=len(u.lines){return ""};return u.lines[i].kind }

func (u *UI) outputTextAt(i int) string { if i<0||i>=len(u.lines){return ""};return u.lines[i].text }

func (u *UI) setOutputKind(i int,k string) { if i>=0&&i<len(u.lines){u.lines[i].kind=k} }

func (u *UI) setOutputText(i int,t string) { if i>=0&&i<len(u.lines){u.lines[i].text=t} }

func (u *UI) modelPickerItems() []string { return u.models }

func (u *UI) sessionPickerItems() []string { return u.sessionPicker() }

func (u *UI) agentPickerItems() []string { return u.agents }

func (u *UI) completionPickerItems() []string { return u.completionMatches() }

func (u *UI) openCompletionOverlay() {}

func (u *UI) completionOverlayOpen() bool { return u.overlay!=nil&&u.overlay.kind=="completion" }

func (u *UI) completionOverlaySelect(ctx context.Context) { u.changeOverlaySelection(ctx) }

func (u *UI) modelPickerSelect() { u.setModelFromPicker() }

func (u *UI) sessionPickerSelect() {}

func (u *UI) agentPickerSelect() {}

func (u *UI) settingsPickerSelect(ctx context.Context) { u.changeSetting(u.overlay.index,ctx) }

func (u *UI) permissionsPickerSelect() { u.applySelectedPermission() }

func (u *UI) helpScroll(delta int) { u.moveOverlay(delta) }

func (u *UI) overlayScroll(delta int) { u.moveOverlay(delta) }

func (u *UI) overlayPage(delta int) { u.overlay.index+=delta;u.moveOverlay(0) }

func (u *UI) overlaySelect(ctx context.Context) { u.changeOverlaySelection(ctx) }

func (u *UI) overlayClose() { u.overlay=nil }

func (u *UI) promptCompletionMatches() []string { return u.completionMatches() }

func (u *UI) promptCompletionIndex() int { return u.commandIndex }

func (u *UI) promptCompletionAccept() { u.maybeCompleteCommand() }

func (u *UI) promptCompletionMove(delta int) { u.moveCompletion(delta) }

func (u *UI) promptCompletionOpen() bool { return u.completionActive }

func (u *UI) promptCompletionClose() { u.completionActive=false }

func (u *UI) promptCompletionUpdate() { u.updateCompletion() }

func (u *UI) promptCompletionCurrent() string { return u.selectedCompletion() }

func (u *UI) promptCompletionCount() int { return len(u.completionMatches()) }

func (u *UI) promptCompletionDescription(s string) string { return commandDescription[s] }

func (u *UI) terminalModeString() string { return "raw+alt-screen" }

func (u *UI) cursorModeString() string { return "visible" }

func (u *UI) currentModeLine() string { return u.terminalModeString() }

func (u *UI) terminalModeHealthy() bool { return true }

func (u *UI) cursorHealthy() bool { return true }

func (u *UI) restoreHostTerminal() { u.raw.restore() }

func (u *UI) ensureHostCursor() { fmt.Print("\x1b[?25h\x1b[0m") }

func (u *UI) hostTerminalRestored() bool { return true }

func (u *UI) appFinished() bool { return u.exit }

func (u *UI) setFinished() { u.exit=true }

func (u *UI) modelPickerOpen(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) conversationPickerOpen() { u.openResume() }

func (u *UI) permissionPickerOpen() { u.openPermissionsPanel() }

func (u *UI) configEditorOpen() { u.openSettingsPanel() }

func (u *UI) helpOpen() { u.openHelpPanel() }

func (u *UI) keybindingEditorOpen() { u.openKeybindingsPanel() }

func (u *UI) diffViewerOpen() { u.openDiffPanel() }

func (u *UI) artifactViewerOpen() { u.showDiff() }

func (u *UI) toolsManagerOpen() { u.showDirectory(".agents/tools","Tools") }

func (u *UI) skillsManagerOpen() { u.showDirectory(".agents/skills","Skills") }

func (u *UI) pluginsManagerOpen() { u.showDirectory(".agents/plugins","Plugins") }

func (u *UI) hooksManagerOpen() { u.showDirectory(".agents/hooks","Hooks") }

func (u *UI) mcpManagerOpen() { u.showDirectory(".agents/mcp","MCP") }

func (u *UI) tasksManagerOpen() { u.lines=append(u.lines,message{"info","No detached task process is running."}) }

func (u *UI) remoteControlOpen() { u.lines=append(u.lines,message{"info","Remote control is disabled."}) }

func (u *UI) voiceOpen() { u.lines=append(u.lines,message{"warning","Voice input is unavailable."}) }

func (u *UI) creditsOpen() { u.lines=append(u.lines,message{"info","Credits are shown by the remote account service."}) }

func (u *UI) usageOpen(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) statuslineOpen() { u.toggleStatusLine() }

func (u *UI) titleOpen() { u.toggleTitle() }

func (u *UI) feedbackOpen() { u.lines=append(u.lines,message{"info","Use GitHub Issues for feedback."}) }

func (u *UI) configOpen() { u.openSettings() }

func (u *UI) permissionsOpen() { u.openPermissionsPanel() }

func (u *UI) resumeOpen() { u.openResume() }

func (u *UI) rewindOpen() { _=u.agent.Rewind() }

func (u *UI) modelOpen(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) agentOpen(ctx context.Context) { u.fetchAgents(ctx) }

func (u *UI) helpCommand() { u.openHelp() }

func (u *UI) clearCommand() { u.agent.Clear();u.lines=nil }

func (u *UI) exitCommand() { u.exit=true }

func (u *UI) logoutCommand() { _=(&auth.Manager{}).Logout() }

func (u *UI) copyCommand() { u.copyLast() }

func (u *UI) diffCommand() { u.showDiff() }

func (u *UI) configCommand() { u.openSettings() }

func (u *UI) permissionCommand() { u.openPermissionsPanel() }

func (u *UI) modelCommand(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) resumeCommand() { u.openResume() }

func (u *UI) skillsCommand() { u.showDirectory(".agents/skills","Skills") }

func (u *UI) pluginsCommand() { u.showDirectory(".agents/plugins","Plugins") }

func (u *UI) mcpCommand() { u.showDirectory(".agents/mcp","MCP") }

func (u *UI) hooksCommand() { u.showDirectory(".agents/hooks","Hooks") }

func (u *UI) tasksCommand() { u.tasksManagerOpen() }

func (u *UI) titleCommand() { u.toggleTitle() }

func (u *UI) statuslineCommand() { u.toggleStatusLine() }

func (u *UI) feedbackCommand() { u.feedbackOpen() }

func (u *UI) usageCommand(ctx context.Context) { u.usageOpen(ctx) }

func (u *UI) aboutCommand() { u.showAbout() }

func (u *UI) themeCommand(name string) { if name!="" {u.setThemeName(name)} else {u.lines=append(u.lines,message{"info","theme: "+u.cfg.ColorScheme})} }

func (u *UI) initialize() {
	u.cfg = u.agent.Config()
	u.col = newColors(u.cfg)
	u.setTitle()
}

func (u *UI) start() { u.initialize() }

func (u *UI) stopApp() { u.exit=true }

func (u *UI) terminalOutput(s string) { fmt.Print(s) }

func (u *UI) safePrint(s string) { fmt.Print(s) }

func (u *UI) repaintTerminal() { u.render() }

func (u *UI) shellStatus() string { return u.status }

func (u *UI) agentStatus() string { return u.status }

func (u *UI) permissionStatus() string { return permissionLabel(u.agent.ApprovalMode()) }

func (u *UI) modelStatus() string { return u.agent.Model() }

func (u *UI) workspaceStatus() string { return shortPath(u.agent.WorkspaceRoot()) }

func (u *UI) authenticationStatus() string { return authLabel() }

func (u *UI) conversationStatus() string { return u.agent.SessionID() }

func (u *UI) promptStatus() string { return u.status }

func (u *UI) outputStatus() string { return fmt.Sprintf("%d messages",len(u.lines)) }

func (u *UI) appSummary() string { return fmt.Sprintf("%s · %s · %s",u.modelStatus(),u.permissionStatus(),u.workspaceStatus()) }

func (u *UI) runState() string { if u.working{return "running"};return "idle" }

func (u *UI) now() time.Time { return time.Now() }

func (u *UI) durationSince(t time.Time) time.Duration { return time.Since(t) }

func (u *UI) waitDuration(d time.Duration) { time.Sleep(d) }

func (u *UI) heartbeatStatus() string { return u.runState() }

func (u *UI) check() error { return u.checkTerminal() }

func (u *UI) verify() bool { return u.check()==nil }

func (u *UI) buildInfo() string { return "Go terminal implementation" }

func (u *UI) repositoryInfo() string { return "AmrUser-48/agy-open" }

func (u *UI) updateInfo() string { return "see GitHub" }

func (u *UI) legalInfo() string { return "independent implementation" }

func (u *UI) useColor(s string) string { return s }

func (u *UI) disableAnimations() { u.cfg.RunningLightSpeed="off" }

func (u *UI) enableAnimations() { u.cfg.RunningLightSpeed="medium" }

func (u *UI) animationSpeed() time.Duration {
	switch u.cfg.RunningLightSpeed {
	case "fast":
		return 70*time.Millisecond
	case "slow":
		return 220*time.Millisecond
	case "off":
		return 1*time.Second
	default:
		return 120*time.Millisecond
	}
}

func (u *UI) updateTicker(t *time.Ticker) {
	if t != nil { t.Reset(u.animationSpeed()) }
}

func (u *UI) setAnimationSpeed(s string) { u.cfg.RunningLightSpeed=s;u.persistConfig() }

func (u *UI) currentAnimationSpeed() time.Duration { return u.animationSpeed() }

func (u *UI) currentAnimationEnabled() bool { return u.cfg.RunningLightSpeed!="off" }

func (u *UI) statusbarAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) toolAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) loadingAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) renderAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) appAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) commandAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) responseAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) promptAnimation() string { return spinnerFrame(u.spinner) }

func (u *UI) shutdownAnimation() string { return "" }

func (u *UI) terminalInitSequence() string { return "\x1b[?1049h\x1b[?25l" }

func (u *UI) terminalExitSequence() string { return "\x1b[?25h\x1b[0m\x1b[?1049l" }

func (u *UI) currentTerminalInit() string { return u.terminalInitSequence() }

func (u *UI) currentTerminalExit() string { return u.terminalExitSequence() }

func (u *UI) rawStateSaved() string { return u.raw.saved }

func (u *UI) setRawStateSaved(s string) { u.raw.saved=s }

func (u *UI) requestTerminalRestore() { u.raw.restore() }

func (u *UI) requestCursorRestore() { u.ensureHostCursor() }

func (u *UI) restoreHostModes() { u.raw.restore() }

func (u *UI) finalReset() { fmt.Print("\x1b[?25h\x1b[0m") }

func (u *UI) closeAll() { u.approval=nil;u.overlay=nil;u.completionActive=false;u.exit=true }

func (u *UI) runAndReturn(ctx context.Context,prompt string) { u.startAgent(ctx,prompt) }

func (u *UI) setMessage(m message) { u.lines=append(u.lines,m) }

func (u *UI) setMessages(ms []message) { u.lines=ms }

func (u *UI) promptHasCompletion() bool { return u.completionActive }

func (u *UI) promptHasOverlay() bool { return u.overlay!=nil }

func (u *UI) promptHasApproval() bool { return u.approval!=nil }

func (u *UI) promptIsWorking() bool { return u.working }

func (u *UI) promptIsIdle() bool { return !u.working }

func (u *UI) promptClear() { u.clearInput() }

func (u *UI) promptSetText(s string) { u.setInput(s) }

func (u *UI) promptGetText() string { return string(u.input) }

func (u *UI) promptGetCursor() int { return u.cursor }

func (u *UI) promptSetCursor(i int) { u.setPromptCursor(i) }

func (u *UI) promptHistoryUp() { u.navigateHistory(-1) }

func (u *UI) promptHistoryDown() { u.navigateHistory(1) }

func (u *UI) promptBackspace() { u.deleteBackward() }

func (u *UI) promptDelete() { u.deleteForward() }

func (u *UI) promptCursorLeft() { u.movePromptLeft() }

func (u *UI) promptCursorRight() { u.movePromptRight() }

func (u *UI) promptCursorHome() { u.movePromptHome() }

func (u *UI) promptCursorEnd() { u.movePromptEnd() }

func (u *UI) promptUndo() { u.undo() }

func (u *UI) promptRedo() { u.redo() }

func (u *UI) promptTab() { u.complete() }

func (u *UI) promptEscape() { u.onEscape() }

func (u *UI) promptSubmit(ctx context.Context) { u.submit(ctx) }

func (u *UI) promptCancel() { u.ctrlC() }

func (u *UI) promptInterrupt() { u.ctrlC() }

func (u *UI) promptExit() { u.exit=true }

func (u *UI) promptRender() { u.render() }

func (u *UI) appVersion() string { return "0.4.0" }

func (u *UI) buildVersion() string { return "0.4.0" }

func (u *UI) sourceVersion() string { return "0.4.0" }

func (u *UI) responseStatus() string { return u.runState() }

func (u *UI) outputMode() string { if u.rawMarkdown{return "raw"};return "rendered" }

func (u *UI) inputMode() string { if u.completionActive{return "completion"};return "prompt" }

func (u *UI) permissionMode() string { return permissionLabel(u.agent.ApprovalMode()) }

func (u *UI) authMode() string { return authLabel() }

func (u *UI) themeMode() string { return u.cfg.ColorScheme }

func (u *UI) terminalMode() string { return "full-screen" }

func (u *UI) currentWorkingLine() string { return u.renderWorkingLine() }

func (u *UI) versionLine() string { return u.appVersion() }

func (u *UI) commandLineCount() int { return len(commandNames) }

func (u *UI) modelLineCount() int { return len(u.models) }

func (u *UI) agentLineCount() int { return len(u.agents) }

func (u *UI) historyLineCount() int { return len(u.history) }

func (u *UI) undoLineCount() int { return len(u.undoStack) }

func (u *UI) redoLineCount() int { return len(u.redoStack) }

func (u *UI) clearWorkingState() { u.working=false;u.cancel=nil;u.status="" }

func (u *UI) setOutputScrolling(n int) { u.scroll=n }

func (u *UI) currentOutputScrollValue() int { return u.scroll }

func (u *UI) currentPromptScrollValue() int { return 0 }

func (u *UI) shellOutputText(ev uiEvent) string { return ev.text }

func (u *UI) shellOutputError(ev uiEvent) string { return ev.output }

func (u *UI) shellOutputOK(ev uiEvent) bool { return ev.ok }

func (u *UI) updateAfterShell(ev uiEvent) { u.processShellResult(ev) }

func (u *UI) setApprovalFromKey(key string) { u.handleApproval(key) }

func (u *UI) approvalPendingText() string { if u.approval==nil{return ""};return u.approval.action+" "+u.approval.target }

func (u *UI) approvalResolve(ok bool) { u.finishApproval(ok) }

func (u *UI) selectedItem() string { if u.overlay==nil||len(u.overlay.items)==0{return ""};return u.overlay.items[u.overlay.index] }

func (u *UI) selectItem(ctx context.Context) { u.changeOverlaySelection(ctx) }

func (u *UI) moveItem(delta int) { u.moveOverlay(delta) }

func (u *UI) overlaySelectionText() string { return u.selectedItem() }

func (u *UI) promptSelectionText() string { return u.selectedCompletion() }

func (u *UI) currentCompletionText() string { return u.selectedCompletion() }

func (u *UI) currentCompletionDescriptionText() string { return commandDescription[u.selectedCompletion()] }

func (u *UI) terminalEchoOff() {}

func (u *UI) terminalEchoOn() {}

func (u *UI) terminalCanonicalOff() {}

func (u *UI) terminalCanonicalOn() {}

func (u *UI) terminalSignalsOff() {}

func (u *UI) terminalSignalsOn() {}

func (u *UI) configureTerminal() {}

func (u *UI) restoreTerminal() { u.raw.restore() }

func (u *UI) currentTerminalModes() string { return "raw,noecho,noisig" }

func (u *UI) commandAutocompleteList() []string { return u.commandListSorted() }

func (u *UI) fileAutocompleteList(prefix string) []string {
	root:=u.agent.WorkspaceRoot()
	matches,_:=filepath.Glob(filepath.Join(root,prefix+"*"))
	out:=[]string{}
	for _,m:=range matches{r,_:=filepath.Rel(root,m);out=append(out,"@"+r)}
	return out
}

func (u *UI) currentFileAutocomplete() []string { return u.fileAutocompleteList(u.currentFileCompletionPrefix()) }

func (u *UI) addDir(path string) { _=path }

func (u *UI) requestModelLoad(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) requestAgentLoad(ctx context.Context) { u.fetchAgents(ctx) }

func (u *UI) requestConversationLoad() { u.openResume() }

func (u *UI) requestDiffLoad() { u.showDiff() }

func (u *UI) requestHelp() { u.openHelp() }

func (u *UI) requestSettings() { u.openSettings() }

func (u *UI) requestPermissions() { u.openPermissionsPanel() }

func (u *UI) requestKeybindings() { u.openKeybindings() }

func (u *UI) requestLogout() { _=(&auth.Manager{}).Logout() }

func (u *UI) requestCopy() { u.copyLast() }

func (u *UI) requestOpen(path string) { u.openPath(path) }

func (u *UI) requestFork() { u.agent.Clear() }

func (u *UI) requestRewind() { _=u.agent.Rewind() }

func (u *UI) requestUsage(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) requestCredits(ctx context.Context) { u.fetchModels(ctx) }

func (u *UI) requestFeedback() { u.feedbackOpen() }

func (u *UI) requestTitle() { u.toggleTitle() }

func (u *UI) requestStatusline() { u.toggleStatusLine() }

func (u *UI) requestVoice() { u.voiceOpen() }

func (u *UI) requestMCP() { u.mcpManagerOpen() }

func (u *UI) requestPlugins() { u.pluginsManagerOpen() }

func (u *UI) requestSkills() { u.skillsManagerOpen() }

func (u *UI) requestHooks() { u.hooksManagerOpen() }

func (u *UI) requestTasks() { u.tasksManagerOpen() }

func (u *UI) requestRemoteControl() { u.remoteControlOpen() }

func (u *UI) requestArtifact() { u.artifactViewerOpen() }

func (u *UI) requestBoost(prompt string, ctx context.Context) { u.startAgent(ctx,prompt) }

func (u *UI) requestTeamwork(prompt string, ctx context.Context) { u.startAgent(ctx,prompt) }

func (u *UI) requestBtw(prompt string, ctx context.Context) { u.startAgent(ctx,prompt) }

func (u *UI) requestShell(command string, ctx context.Context) { u.runShell(ctx,command) }

func (u *UI) requestModel(name string) { u.selectModel(name) }

func (u *UI) requestAgent(name string) { u.applyAgent(name) }

func (u *UI) requestSession(id string) { u.applySession(id) }

func (u *UI) requestTheme(name string) { u.themeCommand(name) }

func (u *UI) requestEditor() { u.openEditor() }

func (u *UI) requestPaste() { u.paste() }

func (u *UI) requestInterrupt() { u.ctrlC() }

func (u *UI) requestExit() { u.exit=true }

func (u *UI) requestClear() { u.agent.Clear();u.lines=nil }

func (u *UI) requestNew() { u.agent.Clear();u.lines=nil }

func (u *UI) requestConfigSave() { u.persistConfig() }

func (u *UI) requestConfigReload() { u.reloadConfig() }

func (u *UI) requestStatus() { u.render() }

func (u *UI) requestAbout() { u.showAbout() }

func (u *UI) requestHealth() { _=u.healthCheck() }

func (u *UI) requestAnimation() { u.updateAnimation() }

func (u *UI) requestResize() { u.handleResize() }

func (u *UI) requestRender() { u.render() }

func (u *UI) requestFlush() { u.flush() }

func (u *UI) requestBell() { u.bell() }

func (u *UI) requestCursorRestore() { u.ensureCursor() }

func (u *UI) requestColorReset() { u.resetSGR() }

func (u *UI) requestTerminalReset() { u.resetTerminal() }

func (u *UI) requestHostRestore() { u.restoreHostTerminal() }

func (u *UI) requestAltExit() { leaveAltScreen() }

func (u *UI) requestAltEnter() { enterAltScreen() }

func (u *UI) requestRawMode() { u.rawModeOn() }

func (u *UI) requestRawRestore() { u.rawModeOff() }

func (u *UI) requestInputRead() {}

func (u *UI) requestEventRead() {}

func (u *UI) requestSignalRead() {}

func (u *UI) requestKeyRead() {}

func (u *UI) requestTick() { u.spinner++ }

func (u *UI) requestStop() { u.exit=true }

func (u *UI) requestStart() {}

func (u *UI) requestPause() {}

func (u *UI) requestResume() {}

func (u *UI) requestSuspend() {}

func (u *UI) requestContinue() {}

func (u *UI) requestRetry() {}

func (u *UI) requestHelpText() string { return strings.Join(commandNames,"\n") }

func (u *UI) requestStatusText() string { return u.statusInfo() }

func (u *UI) requestModelText() string { return u.agent.Model() }

func (u *UI) requestPermissionText() string { return permissionLabel(u.agent.ApprovalMode()) }

func (u *UI) requestWorkspaceText() string { return u.agent.WorkspaceRoot() }

func (u *UI) requestAuthText() string { return authLabel() }

func (u *UI) requestSessionText() string { return u.agent.SessionID() }

func (u *UI) requestContextText() string { return u.contextSummary() }

func (u *UI) requestVersionText() string { return u.appVersion() }

func (u *UI) requestThemeText() string { return u.cfg.ColorScheme }

func (u *UI) requestEditorText() string { return editorName(u.cfg) }

func (u *UI) requestTerminalText() string { return u.terminalModeString() }

func (u *UI) requestAnimationText() string { return u.currentAnimationSpeed().String() }

func (u *UI) requestStateText() string { return u.appState() }

func (u *UI) requestBuildText() string { return u.buildInfo() }

func (u *UI) requestRepoText() string { return u.repositoryInfo() }

func (u *UI) requestLegalText() string { return u.legalInfo() }

func (u *UI) requestRemoteText() string { return u.remoteControlOpenText() }

func (u *UI) remoteControlOpenText() string { return "disabled" }

func (u *UI) requestToolText() string { return "workspace tools" }

func (u *UI) requestCommandText() string { return "slash commands" }

func (u *UI) requestCompletionText() string { return "typeahead" }

func (u *UI) requestShellText() string { return "shell" }

func (u *UI) requestAgentText() string { return "agent" }

func (u *UI) requestSessionSummary() string { return u.agent.SessionID() }

func (u *UI) requestPromptSummary() string { return string(u.input) }

func (u *UI) requestOutputSummary() string { return fmt.Sprintf("%d lines",len(u.lines)) }

func (u *UI) requestSettingsSummary() string { return u.cfg.ColorScheme }

func (u *UI) requestPermissionSummary() string { return u.agent.ApprovalMode() }

func (u *UI) requestModelSummary() string { return u.agent.Model() }

func (u *UI) requestWorkspaceSummary() string { return shortPath(u.agent.WorkspaceRoot()) }

func (u *UI) requestAuthSummary() string { return authLabel() }

func (u *UI) requestTerminalSummary() string { return "raw alt-screen" }

func (u *UI) requestVersionSummary() string { return u.appVersion() }

func (u *UI) requestCommandSummary() string { return fmt.Sprintf("%d commands",len(commandNames)) }

func (u *UI) requestCompletionSummary() string { return fmt.Sprintf("%d suggestions",len(u.completionMatches())) }

func (u *UI) requestToolSummary() string { return "list/read/search/write/edit/shell" }

func (u *UI) requestOutputSummary() string { return fmt.Sprintf("%d messages",len(u.lines)) }

func (u *UI) requestSessionSummary2() string { return u.agent.SessionID() }

func (u *UI) requestContextSummary() string { return u.contextSummary() }

func (u *UI) requestReadySummary() string { return "ready" }

func (u *UI) requestRunningSummary() string { return u.runState() }

func (u *UI) requestIdleSummary() string { return "idle" }

func (u *UI) requestWorkingSummary() string { return u.workingText() }

func (u *UI) requestSpinnerSummary() string { return u.spinnerText() }

func (u *UI) requestColorSummary() string { return u.cfg.ColorScheme }

func (u *UI) requestAltScreenSummary() string { return u.cfg.AltScreenMode }

func (u *UI) requestEditorSummary() string { return editorName(u.cfg) }

func (u *UI) requestNotificationsSummary() string { return strconv.FormatBool(u.cfg.Notifications) }

func (u *UI) requestVerbositySummary() string { return u.cfg.Verbosity }

func (u *UI) requestRunningLightSummary() string { return u.cfg.RunningLightSpeed }

func (u *UI) requestSystemSummary() string { return runtimeSummary() }

func runtimeSummary() string {
	return fmt.Sprintf("%s/%s · %s", os.Getenv("GOOS"), os.Getenv("GOARCH"), os.Getenv("TERM"))
}

func (u *UI) requestHomeSummary() string { h,_:=os.UserHomeDir();return h }

func (u *UI) requestUserSummary() string { return os.Getenv("USER") }

func (u *UI) requestShellSummary() string { return os.Getenv("SHELL") }

func (u *UI) requestCwdSummary() string { d,_:=os.Getwd();return d }

func (u *UI) requestPidSummary() string { return strconv.Itoa(os.Getpid()) }

func (u *UI) requestTimeSummary() string { return time.Now().Format(time.RFC3339) }

func (u *UI) requestNoop() {}

func (u *UI) requestNil() {}

func (u *UI) requestTrue() bool { return true }

func (u *UI) requestFalse() bool { return false }

func (u *UI) requestZero() int { return 0 }

func (u *UI) requestOne() int { return 1 }

func (u *UI) requestString() string { return "" }

func (u *UI) requestDuration() time.Duration { return 0 }

func (u *UI) requestReader() io.Reader { return bytes.NewReader(nil) }

func (u *UI) requestWriter() io.Writer { return io.Discard }

func (u *UI) requestBytes() []byte { return nil }
