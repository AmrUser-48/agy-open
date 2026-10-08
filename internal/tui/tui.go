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
	title  string
	items  []string
	index  int
	kind   string
	footer string
}

type colors struct {
	on     bool
	dim    string
	bold   string
	cyan   string
	green  string
	yellow string
	red    string
	blue   string
	invert string
	reset  string
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
	"/add-dir": "Add a directory to the active workspace",
	"/agents": "Open the agent manager",
	"/boost": "Run a deep reasoning task",
	"/artifact": "Open artifact review",
	"/btw": "Ask a side question",
	"/clear": "Clear the conversation and screen",
	"/config": "Open the settings editor",
	"/context": "Show context usage",
	"/copy": "Copy the last agent response",
	"/credits": "Show account credits",
	"/diff": "Open the working-tree diff",
	"/exit": "Exit the CLI",
	"/fast": "Use fast reasoning",
	"/feedback": "Show feedback information",
	"/fork": "Fork the conversation context",
	"/help": "Show commands and shortcuts",
	"/hooks": "Show hooks",
	"/keybindings": "Show keyboard shortcuts",
	"/logout": "Log out",
	"/mcp": "Open MCP manager",
	"/model": "Select a reasoning model",
	"/open": "Open a file in the external editor",
	"/permissions": "Set tool permission mode",
	"/planning": "Use high-effort planning",
	"/plugin": "Open plugin manager",
	"/rename": "Rename the conversation",
	"/remote-control": "Remote-control status",
	"/resume": "Open the conversation picker",
	"/rewind": "Roll back one conversation turn",
	"/skills": "Browse agent skills",
	"/statusline": "Toggle status line",
	"/tasks": "Show background tasks",
	"/teamwork-preview": "Run a team task",
	"/title": "Toggle the terminal title",
	"/usage": "Show model usage",
	"/voice": "Voice input",
}

type UI struct {
	agent *agent.Agent
	cfg   config.Config
	col   colors

	lines []message
	input []rune
	cursor int

	history      []string
	historyIndex int
	scroll       int
	undoStack    []string
	redoStack    []string

	working bool
	cancel  context.CancelFunc
	status  string
	spinner int
	lastCtrlC time.Time

	completionActive bool
	completionIndex  int

	overlay  *overlay
	approval *approvalRequest

	events chan uiEvent
	keys   chan string
	resize chan os.Signal

	showStatus bool
	title      bool
	trajectory bool
	exit       bool

	models []string
	raw    rawState
}

func New(a *agent.Agent) *UI {
	cfg := a.Config()
	u := &UI{
		agent:       a,
		cfg:         cfg,
		col:         newColors(cfg),
		events:      make(chan uiEvent, 64),
		keys:        make(chan string, 32),
		resize:      make(chan os.Signal, 1),
		historyIndex: -1,
		showStatus:  true,
		title:       true,
	}
	sort.Strings(commandNames)
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

	useAlt := u.cfg.AltScreenMode != "never"
	if useAlt {
		enterAltScreen()
	}
	fmt.Print("\x1b[?25l\x1b[0m")
	u.setTitle()

	signal.Notify(u.resize, syscall.SIGWINCH)
	defer signal.Stop(u.resize)
	defer func() {
		if u.cancel != nil {
			u.cancel()
		}
		if u.approval != nil {
			select {
			case u.approval.reply <- false:
			default:
			}
		}
		fmt.Print("\x1b[?25h\x1b[0m")
		if useAlt {
			leaveAltScreen()
		}
		u.raw.restore()
	}()

	u.lines = append(u.lines,
		message{"info", "agy-open  ·  terminal agent"},
		message{"dim", "workspace  " + u.agent.WorkspaceRoot()},
		message{"dim", "model      " + u.agent.Model()},
		message{"hint", "Type / for commands · @ for files · ! for shell"},
		message{"hint", "Ctrl+C cancel · Ctrl+C again to exit · Ctrl+D exit on empty prompt"},
	)

	go u.keyLoop()
	u.agent.SetConfirm(u.confirm)
	u.agent.SetEventSink(u.agentEvent)
	u.render()

	ticker := time.NewTicker(u.animationSpeed())
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
			ticker.Reset(u.animationSpeed())
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
			break
		}
		if !u.working {
			u.submit(ctx)
		}
	case "CTRL-J", "SHIFT-ENTER":
		u.saveUndo()
		u.input = append(u.input[:u.cursor], append([]rune{'\n'}, u.input[u.cursor:]...)...)
		u.cursor++
		u.completionActive = false
	case "TAB":
		if !u.tryAcceptCompletion() {
			u.complete()
		}
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
	case "LEFT", "CTRL-B":
		if u.cursor > 0 {
			u.cursor--
		}
	case "RIGHT", "CTRL-F":
		if u.cursor < len(u.input) {
			u.cursor++
		}
	case "CTRL-A", "HOME":
		u.cursor = 0
	case "CTRL-E", "END":
		u.cursor = len(u.input)
	case "BACKSPACE", "CTRL-H":
		u.saveUndo()
		u.deleteBackward()
	case "CTRL-D":
		if len(u.input) == 0 {
			u.exit = true
		} else {
			u.saveUndo()
			u.deleteForward()
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
		if !u.working {
			u.openEditor()
		}
	case "CTRL-O":
		u.trajectory = !u.trajectory
	case "CTRL-R":
		if !u.working {
			u.showDiff()
		}
	case "CTRL-Y":
		if u.agent.ApprovalMode() == "auto" {
			_ = u.agent.SetApproval("request-review")
		} else {
			_ = u.agent.SetApproval("always-proceed")
		}
		u.persistConfig()
	case "SHIFT-TAB":
		if !u.working {
			u.cyclePermission()
		}
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
		}
		u.working = false
		u.cancel = nil
		u.status = ""
		u.lines = append(u.lines, message{"warning", "Interrupted"})
		if u.approval != nil {
			select {
			case u.approval.reply <- false:
			default:
			}
			u.approval = nil
		}
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
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	u.history = append(u.history, text)
	u.historyIndex = -1
	u.input = nil
	u.cursor = 0
	u.completionActive = false

	if strings.HasPrefix(text, "/") {
		u.command(text, ctx)
		return
	}
	if strings.HasPrefix(text, "!") {
		u.startShell(ctx, strings.TrimSpace(strings.TrimPrefix(text, "!")))
		return
	}
	u.startAgent(ctx, text)
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
		u.events <- uiEvent{kind: "done", text: out.String(), output: errorText(err), ok: err == nil}
	}()
}

func (u *UI) agentEvent(e agent.Event) {
	u.events <- uiEvent{
		kind: e.Kind, tool: e.Tool, target: e.Target, output: e.Output, ok: e.OK,
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
		mark, kind := "✓", "success"
		if !ev.ok {
			mark, kind = "✗", "error"
		}
		u.lines = append(u.lines, message{kind, mark + " " + label})
		if u.trajectory && strings.TrimSpace(ev.output) != "" {
			u.addBlock("tool output", ev.output, "dim")
		}
	case "approval":
		u.approval = ev.approval
		u.status = "Waiting for approval"
	case "models":
		u.status = ""
		u.models = strings.Split(ev.text, "\x00")
		if ev.ok && len(u.models) > 0 {
			u.overlay = &overlay{title: "Models", items: u.models, kind: "models", footer: "Enter select · Esc close"}
		} else {
			u.lines = append(u.lines, message{"error", ev.text})
		}
	case "message":
		u.status = ""
		kind := "info"
		if !ev.ok {
			kind = "error"
		}
		u.lines = append(u.lines, message{kind, ev.text})
	case "shell_done":
		u.working = false
		u.cancel = nil
		u.status = ""
		if ev.ok {
			if s := strings.TrimSpace(ev.text); s != "" {
				u.addBlock("shell", s, "shell")
			}
		} else if ev.output != "" {
			u.lines = append(u.lines, message{"error", ev.output})
		}
	case "done":
		u.working = false
		u.cancel = nil
		u.status = ""
		if ev.ok {
			if s := strings.TrimSpace(ev.text); s != "" {
				u.addBlock("agy", s, "agent")
			}
		} else if ev.output != "" {
			if strings.Contains(strings.ToLower(ev.output), "canceled") ||
				strings.Contains(strings.ToLower(ev.output), "context canceled") {
				u.lines = append(u.lines, message{"warning", "Interrupted"})
			} else {
				u.lines = append(u.lines, message{"error", ev.output})
			}
		}
		if u.cfg.Notifications {
			fmt.Print("\a")
		}
	}
}

func (u *UI) handleApproval(key string) {
	switch key {
	case "RUNE:y", "RUNE:Y":
		select {
		case u.approval.reply <- true:
		default:
		}
		u.lines = append(u.lines, message{"success", "Approved"})
		u.approval = nil
	case "RUNE:n", "RUNE:N", "ENTER", "ESC":
		select {
		case u.approval.reply <- false:
		default:
		}
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
	case "/model":
		if arg == "" {
			u.fetchModels(ctx)
		} else {
			u.selectModel(arg)
		}
	case "/fast":
		_ = u.agent.SetEffort("low")
		u.cfg.Effort = "low"
		u.persistConfig()
	case "/planning":
		_ = u.agent.SetEffort("high")
		u.cfg.Effort = "high"
		u.persistConfig()
	case "/permissions":
		u.overlay = &overlay{
			title: "Permissions",
			items: []string{"request-review", "proceed-in-sandbox", "always-proceed", "strict"},
			kind: "permissions",
			footer: "Enter apply · Esc close",
			index: permissionIndex(u.agent.ApprovalMode()),
		}
	case "/ask":
		_ = u.agent.SetApproval("request-review")
		u.persistConfig()
	case "/approve":
		_ = u.agent.SetApproval("always-proceed")
		u.persistConfig()
	case "/resume", "/switch", "/conversation":
		if arg == "" {
			u.openResume()
		} else if err := u.agent.ResumeSession(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		}
	case "/rewind", "/undo":
		if err := u.agent.Rewind(); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Conversation rewound"})
		}
	case "/diff":
		u.showDiff()
	case "/copy":
		u.copyLast()
	case "/open":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /open <path>"})
		} else if !u.working {
			u.openPath(arg)
		}
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
	case "/title":
		switch strings.ToLower(arg) {
		case "on":
			u.title = true
		case "off":
			u.title = false
		default:
			u.title = !u.title
		}
		u.setTitle()
	case "/agents":
		u.fetchAgents()
	case "/hooks":
		u.openDirectory(".agents/hooks", "Hooks")
	case "/mcp":
		u.openDirectory(".agents/mcp", "MCP")
	case "/plugin", "/plugins":
		u.openDirectory(".agents/plugins", "Plugins")
	case "/skills":
		u.openDirectory(".agents/skills", "Skills")
	case "/artifact":
		u.showDiff()
	case "/tasks":
		u.lines = append(u.lines, message{"info", "Foreground tool activity is shown above."})
	case "/usage", "/quota", "/credits":
		u.lines = append(u.lines, message{"info", "Usage is provided by the Google account service; model availability is shown by /model."})
	case "/feedback":
		u.lines = append(u.lines, message{"info", "Feedback: https://github.com/AmrUser-48/agy-open/issues"})
	case "/rename":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /rename <name>"})
		} else {
			u.lines = append(u.lines, message{"success", "Conversation title: "+arg})
		}
	case "/fork", "/branch":
		u.agent.Clear()
		u.lines = append(u.lines, message{"success", "Started a new conversation context"})
	case "/boost", "/teamwork-preview", "/teamwork", "/btw":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: "+cmd+" <task>"})
		} else {
			u.startAgent(ctx, arg)
		}
	case "/add-dir":
		u.lines = append(u.lines, message{"warning", "Additional roots are not yet supported by the workspace security boundary."})
	case "/context":
		u.overlay = &overlay{
			title: "Context",
			items: []string{
				fmt.Sprintf("Approximate context characters: %d", u.agent.ContextChars()),
				"Conversation: " + u.agent.SessionID(),
				"Model: " + u.agent.Model(),
			},
			kind: "info",
			footer: "Esc close",
		}
	case "/remote-control":
		u.lines = append(u.lines, message{"info", "Remote control is not configured in this standalone build."})
	case "/voice", "/record":
		u.lines = append(u.lines, message{"warning", "Voice input is unavailable."})
	default:
		u.lines = append(u.lines, message{"error", "Unknown command: " + cmd})
	}
}

func permissionIndex(mode string) int {
	switch mode {
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
	i := (permissionIndex(u.agent.ApprovalMode()) + 1) % len(modes)
	_ = u.agent.SetApproval(modes[i])
	u.cfg.ApprovalMode = u.agent.ApprovalMode()
	u.persistConfig()
}

func (u *UI) openHelp() {
	items := make([]string, 0, len(commandNames))
	for _, name := range commandNames {
		desc := commandDescription[name]
		if desc == "" {
			desc = "Compatibility command"
		}
		items = append(items, fmt.Sprintf("%-20s %s", name, desc))
	}
	u.overlay = &overlay{title: "Commands", items: items, kind: "info", footer: "↑/↓ scroll · Esc close"}
}

func (u *UI) openKeybindings() {
	items := []string{
		"Enter              submit / select",
		"Shift+Enter/Ctrl+J newline",
		"Ctrl+C             interrupt; second press exits",
		"Ctrl+D             forward delete; empty prompt exits",
		"Ctrl+L             clear visual screen",
		"Ctrl+A/Home        prompt start",
		"Ctrl+E/End         prompt end",
		"Ctrl+B/Ctrl+F      move left/right",
		"Ctrl+W             delete previous word",
		"Ctrl+U/Ctrl+K      delete to start/end",
		"Ctrl+G             external editor",
		"Ctrl+V             paste",
		"Ctrl+O             tool trajectory",
		"Ctrl+R             git diff",
		"Ctrl+Y             auto approval",
		"Shift+Tab          permission mode",
		"Alt+Z/Alt+Y        undo/redo",
		"Tab                autocomplete",
		"↑/↓                history or menu",
		"PgUp/PgDn          scroll output",
	}
	u.overlay = &overlay{title: "Keybindings", items: items, kind: "info", footer: "Esc close"}
}

func (u *UI) openSettings() {
	items := []string{
		"model         = " + u.agent.Model(),
		"effort        = " + u.agent.Effort(),
		"permissions   = " + permissionLabel(u.agent.ApprovalMode()),
		"colorScheme   = " + u.cfg.ColorScheme,
		"altScreenMode = " + u.cfg.AltScreenMode,
		"notifications = " + strconv.FormatBool(u.cfg.Notifications),
		"verbosity     = " + u.cfg.Verbosity,
		"runningLight  = " + u.cfg.RunningLightSpeed,
		"editor        = " + editorName(u.cfg),
	}
	u.overlay = &overlay{title: "Settings", items: items, kind: "settings", footer: "↑/↓ select · Enter change · Esc close"}
}

func (u *UI) handleOverlay(ctx context.Context, key string) {
	o := u.overlay
	if o == nil {
		return
	}
	switch key {
	case "ESC", "RUNE:q", "RUNE:Q":
		u.overlay = nil
	case "UP":
		if o.index > 0 {
			o.index--
		}
	case "DOWN":
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
			o.index = len(o.items)-1
		}
	case "ENTER":
		u.changeOverlaySelection(ctx)
	}
	u.render()
}

func (u *UI) changeOverlaySelection(ctx context.Context) {
	o := u.overlay
	if o == nil || len(o.items) == 0 {
		return
	}
	switch o.kind {
	case "models":
		u.selectModel(o.items[o.index])
		u.overlay = nil
	case "permissions":
		modes := []string{"request-review", "proceed-in-sandbox", "always-proceed", "strict"}
		_ = u.agent.SetApproval(modes[o.index])
		u.cfg.ApprovalMode = u.agent.ApprovalMode()
		u.persistConfig()
	case "resume":
		id := o.items[o.index]
		if err := u.agent.ResumeSession(id); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.lines = append(u.lines, message{"success", "Resumed "+id})
			u.overlay = nil
		}
	case "completion":
		item := o.items[o.index]
		before := string(u.input[:u.cursor])
		idx := strings.LastIndex(before, "@")
		if idx >= 0 {
			u.saveUndo()
			u.input = []rune(before[:idx] + item + " " + string(u.input[u.cursor:]))
			u.cursor = len([]rune(before[:idx] + item + " "))
		}
		u.overlay = nil
	case "settings":
		u.changeSetting(o.index, ctx)
	}
}

func (u *UI) changeSetting(index int, ctx context.Context) {
	switch index {
	case 0:
		u.overlay = nil
		u.fetchModels(ctx)
		return
	case 1:
		switch u.agent.Effort() {
		case "low":
			_ = u.agent.SetEffort("medium")
		case "medium":
			_ = u.agent.SetEffort("high")
		default:
			_ = u.agent.SetEffort("low")
		}
		u.cfg.Effort = u.agent.Effort()
	case 2:
		u.cyclePermission()
	case 3:
		schemes := []string{"terminal", "dark", "tokyo night", "solarized dark"}
		for i, s := range schemes {
			if s == u.cfg.ColorScheme {
				u.cfg.ColorScheme = schemes[(i+1)%len(schemes)]
				break
			}
		}
	case 4:
		switch u.cfg.AltScreenMode {
		case "always":
			u.cfg.AltScreenMode = "default"
		case "default":
			u.cfg.AltScreenMode = "never"
		default:
			u.cfg.AltScreenMode = "always"
		}
	case 5:
		u.cfg.Notifications = !u.cfg.Notifications
	case 6:
		if u.cfg.Verbosity == "high" {
			u.cfg.Verbosity = "low"
		} else {
			u.cfg.Verbosity = "high"
		}
	case 7:
		speeds := []string{"fast", "medium", "slow", "off"}
		for i, s := range speeds {
			if s == u.cfg.RunningLightSpeed {
				u.cfg.RunningLightSpeed = speeds[(i+1)%len(speeds)]
				break
			}
		}
	case 8:
		switch editorName(u.cfg) {
		case "vi":
			u.cfg.Editor = "emacs"
		case "emacs":
			u.cfg.Editor = "auto"
		default:
			u.cfg.Editor = "vi"
		}
	}
	u.col = newColors(u.cfg)
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
			u.events <- uiEvent{kind: "models", text: err.Error(), ok: false}
			return
		}
		u.events <- uiEvent{kind: "models", text: strings.Join(models, "\x00"), ok: true}
	}()
}

func (u *UI) fetchAgents() {
	u.overlay = &overlay{title: "Agents", items: []string{"default"}, kind: "info", footer: "Esc close"}
}

func (u *UI) selectModel(name string) {
	if err := u.agent.SetModel(name); err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	u.cfg.Model = name
	u.persistConfig()
	u.setTitle()
	u.lines = append(u.lines, message{"success", "Model: "+name})
}

func (u *UI) openResume() {
	entries, err := u.agent.SessionList(16)
	if err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
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

func (u *UI) openDirectory(rel, title string) {
	root := filepath.Join(u.agent.WorkspaceRoot(), rel)
	entries, err := os.ReadDir(root)
	if err != nil {
		u.lines = append(u.lines, message{"info", title + ": none found"})
		return
	}
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.Name())
	}
	if len(items) == 0 {
		items = []string{"(empty)"}
	}
	u.overlay = &overlay{title: title, items: items, kind: "info", footer: "Esc close"}
}

func (u *UI) showDiff() {
	cmd := exec.Command("git", "-C", u.agent.WorkspaceRoot(), "diff", "--no-ext-diff")
	b, err := cmd.CombinedOutput()
	if err != nil {
		u.lines = append(u.lines, message{"error", "git diff: "+err.Error()})
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		s = "(no changes)"
	}
	u.addBlock("git diff", s, "diff")
}

func (u *UI) startShell(ctx context.Context, command string) {
	if strings.TrimSpace(command) == "" {
		return
	}
	u.lines = append(u.lines, message{"shell", "$ "+command})
	u.working = true
	u.status = "Waiting for approval"
	runCtx, cancel := context.WithCancel(ctx)
	u.cancel = cancel
	go func() {
		if !u.confirm("shell", command) {
			u.events <- uiEvent{kind: "shell_done", output: "shell denied", ok: false}
			return
		}
		u.events <- uiEvent{kind: "message", text: "Running shell", ok: true}
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
	f, err := os.CreateTemp("", "agy-prompt-*.txt")
	if err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	name := f.Name()
	_, _ = f.WriteString(string(u.input))
	_ = f.Close()
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
	if rawErr == nil {
		u.raw = next
	} else {
		u.lines = append(u.lines, message{"error", rawErr.Error()})
	}
	if err != nil {
		u.lines = append(u.lines, message{"error", program+": "+err.Error()})
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
		u.lines = append(u.lines, message{"warning", "No response to copy"})
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
			u.lines = append(u.lines, message{"success", "Copied last response"})
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
	if strings.HasPrefix(before, "/") {
		if strings.Contains(before, " ") {
			fields := strings.Fields(before)
			if len(fields) == 2 && fields[0] == "/model" && len(u.models) > 0 {
				prefix := fields[1]
				items := []string{}
				for _, m := range u.models {
					if strings.HasPrefix(m, prefix) {
						items = append(items, m)
					}
				}
				if len(items) > 0 {
					u.overlay = &overlay{title: "Models", items: items, kind: "models", footer: "Enter select · Esc close"}
				}
			}
			return
		}
		u.updateCompletion()
		return
	}
	idx := strings.LastIndex(before, "@")
	if idx < 0 {
		return
	}
	prefix := before[idx+1:]
	matches, _ := filepath.Glob(filepath.Join(u.agent.WorkspaceRoot(), prefix+"*"))
	if len(matches) == 1 {
		rel, err := filepath.Rel(u.agent.WorkspaceRoot(), matches[0])
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
			rel, _ := filepath.Rel(u.agent.WorkspaceRoot(), m)
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
	u.completionIndex = 0
	u.completionActive = len(u.completionMatches()) > 0
}

func (u *UI) completionMatches() []string {
	prefix := string(u.input)
	matches := make([]string, 0, len(commandNames))
	for _, name := range commandNames {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	return matches
}

func (u *UI) tryAcceptCompletion() bool {
	if !u.completionActive {
		return false
	}
	matches := u.completionMatches()
	if len(matches) == 0 {
		u.completionActive = false
		return false
	}
	if u.completionIndex >= len(matches) {
		u.completionIndex = len(matches)-1
	}
	u.saveUndo()
	u.input = []rune(matches[u.completionIndex])
	u.cursor = len(u.input)
	u.completionActive = false
	return true
}

func (u *UI) moveCompletion(delta int) {
	matches := u.completionMatches()
	if len(matches) == 0 {
		u.completionActive = false
		return
	}
	u.completionIndex = (u.completionIndex + delta) % len(matches)
	if u.completionIndex < 0 {
		u.completionIndex += len(matches)
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
	s := string(u.input)
	if len(u.undoStack) > 0 && u.undoStack[len(u.undoStack)-1] == s {
		return
	}
	u.undoStack = append(u.undoStack, s)
	u.redoStack = nil
	if len(u.undoStack) > 100 {
		u.undoStack = u.undoStack[len(u.undoStack)-100:]
	}
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
	start := u.cursor
	for start > 0 && (u.input[start-1] == ' ' || u.input[start-1] == '\n' || u.input[start-1] == '\t') {
		start--
	}
	for start > 0 && u.input[start-1] != ' ' && u.input[start-1] != '\n' && u.input[start-1] != '\t' {
		start--
	}
	u.saveUndo()
	u.input = append(u.input[:start], u.input[u.cursor:]...)
	u.cursor = start
}

func (u *UI) addBlock(title, text, kind string) {
	if title != "" {
		u.lines = append(u.lines, message{kind, title})
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		u.lines = append(u.lines, message{kind, line})
	}
}

func (u *UI) persistConfig() {
	_ = u.agent.SaveConfig()
	u.cfg = u.agent.Config()
	u.col = newColors(u.cfg)
}

func (u *UI) setTitle() {
	if !u.title {
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

func newColors(cfg config.Config) colors {
	_ = cfg
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return colors{}
	}
	return colors{
		on: true, dim: "\x1b[2m", bold: "\x1b[1m",
		cyan: "\x1b[36m", green: "\x1b[32m", yellow: "\x1b[33m",
		red: "\x1b[31m", blue: "\x1b[34m", invert: "\x1b[7m", reset: "\x1b[0m",
	}
}

func (u *UI) animationSpeed() time.Duration {
	switch u.cfg.RunningLightSpeed {
	case "fast":
		return 70 * time.Millisecond
	case "slow":
		return 220 * time.Millisecond
	case "off":
		return time.Second
	default:
		return 120 * time.Millisecond
	}
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

	body := rows - 6
	all := append([]message(nil), u.lines...)
	if u.working {
		all = append(all, message{"working", spinnerFrame(u.spinner) + " " + runningText(u.status)})
	}

	start := len(all) - body
	if start < 0 {
		start = 0
	}
	for i := start; i < len(all) && body > 0; i++ {
		for _, line := range wrapText(all[i].text, cols) {
			if body <= 0 {
				break
			}
			fmt.Print(u.paint(all[i].kind, line))
			fmt.Print("\r\n")
			body--
		}
	}
	for body > 0 {
		fmt.Print("\r\n")
		body--
	}

	u.renderFooter(cols)
	u.renderPrompt(cols)
}

func (u *UI) renderHeader(cols int) {
	left := fmt.Sprintf("%s%sagy-open%s  %s·%s  %s%s%s  %s·%s  %s%s%s",
		u.col.bold, u.col.blue, u.col.reset,
		u.col.dim, u.col.reset,
		u.col.cyan, u.agent.Model(), u.col.reset,
		u.col.dim, u.col.reset,
		u.col.yellow, permissionLabel(u.agent.ApprovalMode()), u.col.reset)

	right := fmt.Sprintf(" %s%s%s  %s%s%s ",
		u.col.green, authLabel(), u.col.reset,
		u.col.dim, shortPath(u.agent.WorkspaceRoot()), u.col.reset)

	fmt.Print(left)
	pad := cols - visualLen(left) - visualLen(right)
	if pad > 0 {
		fmt.Print(strings.Repeat(" ", pad))
	}
	fmt.Print(right)
	fmt.Print("\r\n")
	fmt.Print(strings.Repeat("─", cols))
	fmt.Print("\r\n")
}

func (u *UI) renderFooter(cols int) {
	fmt.Print(strings.Repeat("─", cols))
	fmt.Print("\r\n")
	hint := "Enter send · Tab complete · Ctrl+C cancel/exit · / commands"
	if u.approval != nil {
		hint = fmt.Sprintf("%sAllow%s %s%s%s?  y / n / Enter",
			u.col.yellow, u.col.reset,
			u.col.bold, u.approval.action+" "+u.approval.target, u.col.reset)
	} else if u.completionActive {
		hint = "↑/↓ select · Enter/Tab accept · Esc close"
	}
	fmt.Print(u.paint("hint", clipVisible(hint, cols)))
	fmt.Print("\r\n")
}

func (u *UI) renderPrompt(cols int) {
	lines := strings.Split(string(u.input), "\n")
	fmt.Print(u.col.bold + "› " + u.col.reset)
	for i, line := range lines {
		if i > 0 {
			fmt.Print("\r\n  ")
		}
		fmt.Print(u.paint("prompt", clipVisible(line, max(1, cols-3))))
	}
	fmt.Print("\x1b[?25h")
	last := lines[len(lines)-1]
	fmt.Printf("\x1b[%dG", len([]rune(last))+3)

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
	n := min(8, len(matches))
	for i := 0; i < n; i++ {
		prefix, style := "  ", ""
		if i == u.completionIndex {
			prefix, style = "› ", u.col.invert
		}
		fmt.Print(style + prefix + matches[i] + u.col.reset)
		if desc := commandDescription[matches[i]]; desc != "" {
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
	titleLine := "┌─ " + o.title + " "
	if len(titleLine) < cols-1 {
		titleLine += strings.Repeat("─", cols-len([]rune(titleLine))-1)
	}
	fmt.Print("\r\n" + u.col.bold + titleLine + u.col.reset + "\r\n")

	start := o.index - 5
	if start < 0 {
		start = 0
	}
	n := min(12, len(o.items)-start)
	for i := 0; i < n; i++ {
		idx := start + i
		prefix, style := "  ", ""
		if idx == o.index {
			prefix, style = "› ", u.col.invert
		}
		fmt.Print(style + prefix + clipVisible(o.items[idx], max(1, cols-4)) + u.col.reset + "\r\n")
	}
	fmt.Print(u.col.dim + "└─ " + o.footer + u.col.reset + "\r\n")
}

func (u *UI) paint(kind, text string) string {
	switch kind {
	case "user":
		return u.col.bold + u.col.blue + text + u.col.reset
	case "agent":
		return u.col.green + text + u.col.reset
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
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return "~/" + rel
		}
	}
	return path
}

func wrapText(s string, width int) []string {
	if width < 1 {
		return []string{s}
	}
	out := []string{}
	for _, raw := range strings.Split(s, "\n") {
		r := []rune(raw)
		if len(r) == 0 {
			out = append(out, "")
			continue
		}
		for len(r) > width {
			cut := width
			for i := width; i > width-24 && i > 1; i-- {
				if r[i-1] == ' ' || r[i-1] == '\t' {
					cut = i
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

func clipVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(stripANSI(s))
	if len(r) <= width {
		return s
	}
	return string(r[:width])
}

func visualLen(s string) int {
	return len([]rune(stripANSI(s)))
}

func stripANSI(s string) string {
	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			return s
		}
		endRel := strings.IndexAny(s[start+2:], "ABCDEFGHJKSTfmnsu")
		if endRel < 0 {
			return s
		}
		end := start + 2 + endRel + 1
		s = s[:start] + s[end:]
	}
}

func enterAltScreen() {
	fmt.Print("\x1b[?1049h")
}

func leaveAltScreen() {
	fmt.Print("\x1b[?1049l")
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
	cmd = exec.Command("stty", "-icanon", "-echo", "-isig", "-ixon", "min", "1", "time", "0")
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return rawState{}, err
	}
	return rawState{saved: saved}, nil
}

func (s rawState) restore() {
	if s.saved != "" {
		cmd := exec.Command("stty", s.saved)
		cmd.Stdin = os.Stdin
		if cmd.Run() == nil {
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
	seq := make([]rune, 0, 16)
	for len(seq) < 16 {
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
	switch s {
	case "A":
		return "UP", nil
	case "B":
		return "DOWN", nil
	case "C":
		return "RIGHT", nil
	case "D":
		return "LEFT", nil
	case "H":
		return "HOME", nil
	case "F":
		return "END", nil
	case "Z":
		return "SHIFT-TAB", nil
	case "5~":
		return "PUP", nil
	case "6~":
		return "PDOWN", nil
	case "3~":
		return "CTRL-D", nil
	case "13;2u", "27;2;13~":
		return "SHIFT-ENTER", nil
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

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

var _ io.Writer
var _ session.Entry
