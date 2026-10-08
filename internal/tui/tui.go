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
	"sync"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
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
	models   []gemini.ModelOption
}

type overlay struct {
	title   string
	items   []string
	values  []string
	index   int
	kind    string
	footer  string
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
	"/add-dir", "/agents", "/artifact", "/boost", "/btw", "/clear", "/codesearch",
	"/config", "/context", "/conversation", "/copy", "/credits", "/diff", "/effort",
	"/exit", "/fast", "/feedback", "/fork", "/goal", "/grill-me", "/help", "/hooks",
	"/keybindings", "/learn", "/logout", "/mcp", "/model", "/new", "/open",
	"/permissions", "/plan", "/planning", "/plugin", "/plugins", "/quit",
	"/remote-control", "/rename", "/resume", "/rewind", "/schedule", "/settings", "/branch",
	"/skills", "/statusline", "/switch", "/tasks", "/teamwork", "/teamwork-preview",
	"/title", "/undo", "/usage", "/quota", "/voice", "/record", "/browser",
}

var commandDescription = map[string]string{
	"/add-dir": "Add a directory to the active workspace",
	"/agents": "Open the Agent Manager",
	"/artifact": "Open artifact review",
	"/boost": "Run a deep-reasoning task",
	"/btw": "Ask a side question",
	"/browser": "Browser research (not available in this build)",
	"/clear": "Clear terminal and conversation context",
	"/codesearch": "Search code with the agent",
	"/config": "Open settings (alias: /settings)",
	"/context": "Show context usage",
	"/conversation": "Resume a conversation (alias: /resume)",
	"/copy": "Copy the last agent response",
	"/credits": "Show account credits",
	"/diff": "Open the working-tree diff",
	"/effort": "Set reasoning effort: low, medium, high",
	"/exit": "Exit the CLI",
	"/fast": "Enable fast reasoning mode",
	"/feedback": "Show project feedback information",
	"/fork": "Fork the current conversation (alias: /branch)",
	"/goal": "Run continuously toward a goal",
	"/grill-me": "Interview before executing a task",
	"/help": "Show commands and shortcuts",
	"/hooks": "Browse hook files",
	"/keybindings": "Show keyboard shortcuts",
	"/learn": "Turn session corrections into project rules/skills",
	"/logout": "Log out of Google",
	"/mcp": "Open MCP manager",
	"/model": "Select a model",
	"/new": "Clear conversation (alias: /clear)",
	"/open": "Open a file in the external editor",
	"/permissions": "Set tool permission mode",
	"/plan": "Plan a task before execution",
	"/planning": "Enable high-effort planning mode",
	"/plugin": "Open plugin manager",
	"/plugins": "Alias for /plugin",
	"/quit": "Exit the CLI",
	"/quota": "Alias for /usage",
	"/record": "Alias for /voice",
	"/remote-control": "Remote-control status",
	"/rename": "Rename the current conversation",
	"/resume": "Open the conversation picker",
	"/rewind": "Roll back one conversation turn",
	"/schedule": "Scheduled tasks (not available in this build)",
	"/settings": "Alias for /config",
	"/skills": "Browse agent skills",
	"/statusline": "Customize status line",
	"/switch": "Alias for /resume",
	"/tasks": "Show task activity",
	"/teamwork": "Alias for /teamwork-preview",
	"/teamwork-preview": "Run a collaborative team task",
	"/title": "Toggle terminal title updates",
	"/undo": "Alias for /rewind",
	"/usage": "Display model quota usage",
	"/voice": "Voice input (not available in this build)",
}

type UI struct {
	agent *agent.Agent
	cfg   config.Config
	col   colors

	lines []message
	input []rune
	cursor int

	history    promptHistory
	scrollTop int
	followBottom bool
	undoStack    []string
	redoStack    []string

	working bool
	cancel  context.CancelFunc
	status  string
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
	mouseMode  bool
	trajectory bool
	exit       bool

	modelOptions []gemini.ModelOption
	modelLoading bool
	streaming    bool
	raw          rawState
	termCols      int
	termRows      int
	streamDirty   bool
	streamMu      sync.Mutex
	streamBuf     strings.Builder
	streamText    strings.Builder

	visualCache          []message
	visualCacheCols      int
	visualCacheLines     int
	visualCacheLastStart int
	visualCacheLastText  string
	visualCacheLastKind  string
	visualCacheWorking   bool
	visualCacheStatus    string
	visualCacheValid     bool
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
		history:      newPromptHistory(nil),
		followBottom: true,
		showStatus:   cfg.ShowStatus,
		title:        true,
		mouseMode:    cfg.MouseMode,
		trajectory:   cfg.Trajectory,
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
	u.refreshTerminalSize()

	useAlt := u.cfg.AltScreenMode != "never"
	if useAlt {
		enterAltScreen()
	}
	setMouseReporting(u.mouseMode)
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
		disableMouseReporting()
		if useAlt {
			leaveAltScreen()
		}
		u.raw.restore()
	}()

	if prompts, err := session.RecentPrompts(200); err == nil {
		u.history = newPromptHistory(prompts)
	}

	u.lines = append(u.lines,
		message{"info", "agy-open  ·  terminal agent"},
		message{"dim", "workspace  " + u.agent.WorkspaceRoot()},
		message{"dim", "model      " + u.agent.Model()},
		message{"hint", "Type / for commands · @ for files · ! for shell"},
		message{"hint", "Ctrl+C cancel · Ctrl+C again to exit · Ctrl+D exit on empty prompt"},
	)

	go u.keyLoop()
	u.render()

	streamTicker := time.NewTicker(50 * time.Millisecond)
	defer streamTicker.Stop()

	for !u.exit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case key := <-u.keys:
			u.handleKey(ctx, key)
		case ev := <-u.events:
			if ev.kind == "done" {
				u.flushStream()
			}
			u.handleEvent(ev)
			if ev.kind != "text_delta" {
				u.render()
			}
		case <-streamTicker.C:
			if u.flushStream() {
				u.render()
			}
		case <-u.resize:
			u.refreshTerminalSize()
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
	cols, _ := u.terminalSize()
	beforePromptRows := u.promptRows(cols)
	beforeCompletionRows := u.completionRows()
	beforeCompletionIndex := u.completionIndex
	beforeOverlay := u.overlay != nil
	beforeLineCount := len(u.lines)
	beforeWorking := u.working
	beforeScrollTop := u.scrollTop
	beforeFollowBottom := u.followBottom
	beforeApprovalMode := u.agent.ApprovalMode()

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
		u.history.edit()
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
		u.history.edit()
		u.saveUndo()
		u.deleteBackward()
	case "CTRL-D":
		if len(u.input) != 0 {
			u.history.edit()
		}
		if len(u.input) == 0 {
			u.exit = true
		} else {
			u.saveUndo()
			u.deleteForward()
		}
	case "CTRL-W":
		u.history.edit()
		u.deleteWordBackward()
	case "CTRL-U":
		u.history.edit()
		u.saveUndo()
		u.input = u.input[u.cursor:]
		u.cursor = 0
	case "CTRL-K":
		u.history.edit()
		u.saveUndo()
		u.input = u.input[:u.cursor]
	case "CTRL-L":
		u.followBottom = true
		u.scrollTop = 0
	case "PUP", "MOUSE-UP":
		u.scrollOutput(-max(1, u.visibleRows()/2))
	case "PDOWN", "MOUSE-DOWN":
		u.scrollOutput(max(1, u.visibleRows()/2))
	case "MOUSE-IGNORE":
		// Ignore mouse press/release/motion.
	case "CTRL-C":
		u.ctrlC()
	case "ESC":
		u.completionActive = false
		u.overlay = nil
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
	case "CTRL-S":
		u.mouseMode = !u.mouseMode
		u.cfg.MouseMode = u.mouseMode
		setMouseReporting(u.mouseMode)
		u.persistConfig()
	case "CTRL-O":
		u.trajectory = !u.trajectory
		u.cfg.Trajectory = u.trajectory
		u.persistConfig()
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
				u.history.edit()
				u.saveUndo()
				u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
				u.cursor++
				u.updateCompletion()
			}
		}
	}

	full := beforePromptRows != u.promptRows(cols) ||
		beforeCompletionRows != u.completionRows() ||
		beforeCompletionIndex != u.completionIndex ||
		beforeOverlay != (u.overlay != nil) ||
		beforeLineCount != len(u.lines) ||
		beforeWorking != u.working ||
		beforeScrollTop != u.scrollTop ||
		beforeFollowBottom != u.followBottom ||
		beforeApprovalMode != u.agent.ApprovalMode() ||
		key == "CTRL-L"

	if full {
		u.render()
	} else {
		u.renderPromptOnly(cols)
	}
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
	u.history.add(text)
	u.history.reset()
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
	u.followBottom = true
	u.scrollTop = 0
	u.status = "Thinking"
	u.streamDirty = false
	u.working = true
	u.streaming = true
	runCtx, cancel := context.WithCancel(parent)
	u.cancel = cancel

	u.streamMu.Lock()
	u.streamBuf.Reset()
	u.streamMu.Unlock()
	u.streamText.Reset()
	u.agent.SetTextSink(func(s string) {
		if s == "" {
			return
		}
		u.streamMu.Lock()
		_, _ = u.streamBuf.WriteString(s)
		u.streamMu.Unlock()
	})

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

func (u *UI) takeStreamText() string {
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	if u.streamBuf.Len() == 0 {
		return ""
	}
	text := u.streamBuf.String()
	u.streamBuf.Reset()
	return text
}

func (u *UI) flushStream() bool {
	text := u.takeStreamText()
	if text == "" {
		return false
	}
	u.appendStreamText(text)
	u.streamDirty = false
	return true
}

func (u *UI) appendStreamText(text string) {
	if text == "" {
		return
	}
	if len(u.lines) == 0 || u.lines[len(u.lines)-1].kind != "agent-stream" {
		u.lines = append(u.lines, message{"agent-stream", ""})
		u.streamText.Reset()
	}
	_, _ = u.streamText.WriteString(text)
	u.status = "Responding"
}

func (u *UI) handleEvent(ev uiEvent) {
	switch ev.kind {
	case "text_delta":
		if ev.text != "" {
			u.appendStreamText(ev.text)
		}
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
		u.modelLoading = false
		if !ev.ok || len(ev.models) == 0 {
			u.lines = append(u.lines, message{"error", ev.text})
			break
		}
		u.modelOptions = ev.models
		items := make([]string, 0, len(ev.models))
		values := make([]string, 0, len(ev.models))
		for _, model := range ev.models {
			items = append(items, formatModelOption(model))
			values = append(values, model.ID)
		}
		u.overlay = &overlay{
			title:   "Models",
			items:   items,
			values:  values,
			kind:    "models",
			footer:  "↑/↓ select · Enter apply · Esc close",
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
		u.agent.SetTextSink(nil)
		u.working = false
		u.followBottom = true
		u.streamDirty = false
		u.streaming = false
		u.cancel = nil
		u.status = ""
		streamed := len(u.lines) > 0 && u.lines[len(u.lines)-1].kind == "agent-stream"
		if streamed {
			u.lines[len(u.lines)-1].text = strings.Clone(u.streamText.String())
			u.streamText.Reset()
			if ev.ok {
				u.lines[len(u.lines)-1].kind = "agent"
			}
		} else if ev.ok {
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
		} else if err := u.selectModelArg(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		}
	case "/effort":
		if arg == "" {
			u.lines = append(u.lines, message{"info", "Current effort: "+u.agent.Effort()})
		} else if !u.supportsEffort(arg) {
			u.lines = append(u.lines, message{"error", "Effort "+strings.ToLower(strings.TrimSpace(arg))+" is not supported by "+u.agent.Model()})
		} else if err := u.agent.SetEffort(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.cfg.Effort = u.agent.Effort()
			u.persistConfig()
			u.lines = append(u.lines, message{"success", "Effort: "+u.agent.Effort()})
		}
	case "/config", "/settings":
		u.openSettings()
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
	case "/diff", "/artifact":
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
	case "/permissions":
		if arg != "" {
			switch strings.ToLower(strings.TrimSpace(arg)) {
			case "request-review", "always-proceed", "strict":
				_ = u.agent.SetApproval(arg)
				u.cfg.ApprovalMode = u.agent.ApprovalMode()
				u.persistConfig()
			default:
				u.lines = append(u.lines, message{"error", "usage: /permissions [request-review|always-proceed|strict]"})
			}
			return
		}
		u.overlay = &overlay{
			title: "Permissions",
			items: []string{"request-review", "always-proceed", "strict"},
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
	case "/fast":
		_ = u.agent.SetEffort("low")
		u.cfg.Effort = "low"
		u.persistConfig()
	case "/planning":
		_ = u.agent.SetEffort("high")
		u.cfg.Effort = "high"
		u.persistConfig()
	case "/statusline":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "", "toggle":
			u.showStatus = !u.showStatus
		case "on", "enable":
			u.showStatus = true
		case "off", "disable":
			u.showStatus = false
		case "delete", "reset":
			u.showStatus = true
			u.lines = append(u.lines, message{"info", "Custom statusline commands are not configured in agy-open; restored the built-in status line."})
		case "help":
			u.lines = append(u.lines, message{"info", "/statusline [on|off|enable|disable|delete|reset|help]"})
		default:
			u.lines = append(u.lines, message{"warning", "Custom statusline commands are not implemented; use /statusline on or off."})
			return
		}
		u.cfg.ShowStatus = u.showStatus
		u.persistConfig()
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
		fields := strings.Fields(arg)
		if len(fields) == 0 || fields[0] == "list" {
			u.openDirectory(".agents/plugins", "Plugins")
		} else {
			switch fields[0] {
			case "install", "uninstall", "enable", "disable":
				u.lines = append(u.lines, message{"warning", "Plugin command accepted, but the plugin manager is not implemented in this lightweight build."})
			default:
				u.lines = append(u.lines, message{"error", "usage: /plugin [install|uninstall|enable|disable|list] [name]"})
			}
		}
	case "/skills":
		u.openDirectory(".agents/skills", "Skills")
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
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "", "status":
			u.lines = append(u.lines, message{"info", "Remote control is not configured in this standalone build."})
		case "on", "off":
			u.lines = append(u.lines, message{"warning", "Remote control is not available in agy-open."})
		default:
			u.lines = append(u.lines, message{"error", "usage: /remote-control [on|off]"})
		}
	case "/voice", "/record":
		u.lines = append(u.lines, message{"warning", "Voice input is unavailable in agy-open."})
	case "/plan":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /plan <task>"})
		} else {
			_ = u.agent.SetEffort("high")
			u.cfg.Effort = "high"
			u.persistConfig()
			u.startAgent(ctx, "Plan the following task before making changes: "+arg)
		}
	case "/goal":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /goal <task>"})
		} else {
			_ = u.agent.SetApproval("always-proceed")
			u.persistConfig()
			u.startAgent(ctx, "Work continuously toward this goal and verify the result: "+arg)
		}
	case "/grill-me":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /grill-me <task>"})
		} else {
			u.startAgent(ctx, "Interview me about requirements, trade-offs, and edge cases before implementing: "+arg)
		}
	case "/learn":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /learn <observation>"})
		} else {
			u.startAgent(ctx, "Analyze this session correction and propose a persistent project rule or skill: "+arg)
		}
	case "/codesearch":
		if arg == "" {
			u.lines = append(u.lines, message{"error", "usage: /codesearch <query>"})
		} else {
			u.startAgent(ctx, "Search this workspace for: "+arg)
		}
	case "/browser", "/schedule":
		u.lines = append(u.lines, message{"warning", cmd+" is not available in this lightweight build."})
	default:
		u.lines = append(u.lines, message{"error", "Unknown command: " + cmd})
	}
}

func permissionIndex(mode string) int {
	switch mode {
	case "auto":
		return 1
	case "deny":
		return 2
	default:
		return 0
	}
}

func (u *UI) supportsEffort(level string) bool {
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "low" && level != "medium" && level != "high" {
		return false
	}
	for _, model := range u.modelOptions {
		if strings.EqualFold(model.ID, u.agent.Model()) {
			if len(model.SupportedEfforts) == 0 {
				return true
			}
			for _, supported := range model.SupportedEfforts {
				if strings.EqualFold(supported, level) {
					return true
				}
			}
			return false
		}
	}
	return true
}

func (u *UI) cycleEffort() {
	levels := []string{"low", "medium", "high"}
	for _, model := range u.modelOptions {
		if !strings.EqualFold(model.ID, u.agent.Model()) {
			continue
		}
		if len(model.SupportedEfforts) > 0 {
			levels = model.SupportedEfforts
		}
		break
	}
	current := strings.ToLower(u.agent.Effort())
	for i, level := range levels {
		if strings.EqualFold(level, current) {
			next := levels[(i+1)%len(levels)]
			_ = u.agent.SetEffort(next)
			return
		}
	}
	if len(levels) > 0 {
		_ = u.agent.SetEffort(levels[0])
	}
}

func (u *UI) cyclePermission() {
	modes := []string{"request-review", "always-proceed", "strict"}
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
		"Ctrl+S             toggle mouse mode (off = terminal selection)",
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
		fmt.Sprintf("Model          %s", u.agent.Model()),
		fmt.Sprintf("Reasoning      %s", u.agent.Effort()),
		fmt.Sprintf("Permissions    %s", permissionLabel(u.agent.ApprovalMode())),
		fmt.Sprintf("Theme          %s", u.cfg.ColorScheme),
		fmt.Sprintf("Alt screen     %s", u.cfg.AltScreenMode),
		fmt.Sprintf("Notifications  %s", onOff(u.cfg.Notifications)),
		fmt.Sprintf("Verbosity      %s", u.cfg.Verbosity),
		fmt.Sprintf("Editor         %s", editorName(u.cfg)),
		fmt.Sprintf("Status bar     %s", onOff(u.showStatus)),
		fmt.Sprintf("Trajectory     %s", onOff(u.trajectory)),
		fmt.Sprintf("Mouse mode     %s", onOff(u.mouseMode)),
	}
	u.overlay = &overlay{
		title:  "Configuration",
		items:  items,
		kind:   "settings",
		footer: "↑/↓ select · Enter change · Esc close",
	}
}

func (u *UI) handleOverlay(ctx context.Context, key string) {
	o := u.overlay
	if o == nil {
		return
	}
	switch key {
	case "ESC", "RUNE:q", "RUNE:Q", "CTRL-C":
		u.overlay = nil
	case "UP", "RUNE:k":
		if o.index > 0 {
			o.index--
		}
	case "DOWN", "RUNE:j":
		if o.index+1 < len(o.items) {
			o.index++
		}
	case "PUP", "CTRL-U":
		o.index -= max(1, u.visibleRows()/2)
		if o.index < 0 {
			o.index = 0
		}
	case "PDOWN", "CTRL-D":
		o.index += max(1, u.visibleRows()/2)
		if o.index >= len(o.items) {
			o.index = len(o.items)-1
		}
	case "HOME", "RUNE:g":
		o.index = 0
	case "END", "RUNE:G":
		o.index = max(0, len(o.items)-1)
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
		value := o.items[o.index]
		if o.index < len(o.values) && strings.TrimSpace(o.values[o.index]) != "" {
			value = o.values[o.index]
		}
		u.selectModel(value)
		u.overlay = nil
	case "permissions":
		modes := []string{"request-review", "always-proceed", "strict"}
		_ = u.agent.SetApproval(modes[o.index])
		u.cfg.ApprovalMode = u.agent.ApprovalMode()
		u.persistConfig()
	case "resume":
		id := o.items[o.index]
		if o.index < len(o.values) && strings.TrimSpace(o.values[o.index]) != "" {
			id = o.values[o.index]
		}
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
		u.cycleEffort()
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
		switch editorName(u.cfg) {
		case "vi":
			u.cfg.Editor = "emacs"
		case "emacs":
			u.cfg.Editor = "auto"
		default:
			u.cfg.Editor = "vi"
		}
	case 8:
		u.showStatus = !u.showStatus
		u.cfg.ShowStatus = u.showStatus
	case 9:
		u.trajectory = !u.trajectory
		u.cfg.Trajectory = u.trajectory
	case 10:
		u.mouseMode = !u.mouseMode
		u.cfg.MouseMode = u.mouseMode
		setMouseReporting(u.mouseMode)
	}
	u.col = newColors(u.cfg)
	u.persistConfig()
	u.openSettings()
}

func (u *UI) fetchModels(ctx context.Context) {
	if u.modelLoading {
		return
	}
	u.modelLoading = true
	u.status = "Loading models"
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		models, err := u.agent.ListModelOptions(c)
		if err != nil {
			u.events <- uiEvent{kind: "models", text: err.Error(), ok: false}
			return
		}
		u.events <- uiEvent{kind: "models", models: models, ok: true}
	}()
}


func (u *UI) fetchAgents() {
	u.overlay = &overlay{title: "Agents", items: []string{"default"}, kind: "info", footer: "Esc close"}
}

func formatModelOption(model gemini.ModelOption) string {
	return model.Label()
}

func (u *UI) selectModelArg(name string) error {
	name = strings.TrimSpace(name)
	for _, model := range u.modelOptions {
		if strings.EqualFold(name, model.ID) || strings.EqualFold(name, model.Label()) {
			u.selectModelOption(model)
			return nil
		}
	}
	aliases := map[string]string{
		"gemini-3.8-flash": "gemini-3.8-flash-medium",
		"gemini-3.7-flash": "gemini-3.7-flash-medium",
		"gemini-3.6-flash": "gemini-3.6-flash-medium",
		"gemini-3.1-pro":   "gemini-3.1-pro-high",
	}
	if id, ok := aliases[strings.ToLower(name)]; ok {
		u.selectModel(id)
		return nil
	}
	return fmt.Errorf("unknown model %q; use /model to choose an available model", name)
}

func (u *UI) selectModelOption(model gemini.ModelOption) {
	if err := u.agent.SetModel(model.ID); err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	if len(model.SupportedEfforts) > 0 {
		current := strings.ToLower(u.agent.Effort())
		compatible := false
		for _, effort := range model.SupportedEfforts {
			if strings.EqualFold(effort, current) {
				compatible = true
				break
			}
		}
		if !compatible {
			level := model.DefaultEffort
			if level == "" {
				level = model.SupportedEfforts[0]
			}
			_ = u.agent.SetEffort(level)
			u.cfg.Effort = u.agent.Effort()
		}
	}
	u.cfg.Model = u.agent.Model()
	u.persistConfig()
	u.setTitle()
	u.lines = append(u.lines, message{"success", "Model: "+u.agent.Model()+" · effort "+u.agent.Effort()})
}

func (u *UI) selectModel(name string) {
	name = strings.TrimSpace(name)
	for _, model := range u.modelOptions {
		if strings.EqualFold(model.ID, name) {
			u.selectModelOption(model)
			return
		}
	}
	if err := u.agent.SetModel(name); err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	u.cfg.Model = u.agent.Model()
	u.persistConfig()
	u.setTitle()
	u.lines = append(u.lines, message{"success", "Model: "+u.agent.Model()})
}


func (u *UI) openResume() {
	entries, err := u.agent.SessionList(24)
	if err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	items := make([]string, 0, len(entries))
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		items = append(items, fmt.Sprintf("%s  ·  %s", entry.ID, entry.ModTime.Local().Format("2006-01-02 15:04")))
		values = append(values, entry.ID)
	}
	if len(items) == 0 {
		u.lines = append(u.lines, message{"info", "No saved conversations"})
		return
	}
	u.overlay = &overlay{
		title:  "Conversations",
		items:  items,
		values: values,
		kind:   "resume",
		footer: "↑/↓ select · Enter resume · Esc close",
	}
}

func (u *UI) openDirectory(rel, title string) {
	root := filepath.Join(u.agent.WorkspaceRoot(), rel)
	entries, err := os.ReadDir(root)
	if err != nil {
		u.lines = append(u.lines, message{"info", title + ": none found"})
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entry.Name())
	}
	if len(items) == 0 {
		items = []string{"(empty)"}
	}
	u.overlay = &overlay{title: title, items: items, kind: "info", footer: "↑/↓ select · Esc close"}
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
	fmt.Print("\x1b[?25h\x1b[0m")
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
		if strings.HasPrefix(before, "/model ") {
			u.fetchModels(context.Background())
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
		u.history.edit()
		u.saveUndo()
		u.input = []rune(before[:idx] + repl + " " + text[u.cursor:])
		u.cursor = len([]rune(before[:idx] + repl + " "))
		return
	}
	if len(matches) > 0 {
		items := make([]string, 0, len(matches))
		values := make([]string, 0, len(matches))
		for _, match := range matches {
			rel, _ := filepath.Rel(u.agent.WorkspaceRoot(), match)
			items = append(items, "@"+rel)
			values = append(values, match)
		}
		u.overlay = &overlay{
			title:  "Files",
			items:  items,
			values: values,
			kind:   "completion",
			footer: "Enter insert · Esc close",
		}
	}
}

func (u *UI) updateCompletion() {
	prefix := string(u.input)
	if !strings.HasPrefix(prefix, "/") {
		u.completionActive = false
		return
	}
	if strings.Contains(prefix, " ") {
		commandsWithArgs := []string{"/model ", "/effort ", "/permissions ", "/statusline ", "/remote-control ", "/title ", "/plugin "}
		supported := false
		for _, command := range commandsWithArgs {
			if strings.HasPrefix(prefix, command) {
				supported = true
				break
			}
		}
		if !supported {
			u.completionActive = false
			return
		}
	}
	u.completionIndex = 0
	u.completionActive = len(u.completionMatches()) > 0
}

func (u *UI) completionMatches() []string {
	prefix := string(u.input)
	if strings.HasPrefix(prefix, "/model ") {
		query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(prefix, "/model ")))
		matches := make([]string, 0, len(u.modelOptions))
		for _, model := range u.modelOptions {
			id := strings.ToLower(model.ID)
			label := strings.ToLower(model.Label())
			if query == "" || strings.HasPrefix(id, query) || strings.HasPrefix(label, query) ||
				strings.Contains(id, query) || strings.Contains(label, query) {
				matches = append(matches, "/model "+model.ID)
			}
		}
		return matches
	}
	rules := map[string][]string{
		"/effort ":          {"low", "medium", "high"},
		"/permissions ":     {"request-review", "always-proceed", "strict"},
		"/statusline ":      {"on", "off", "enable", "disable", "delete", "reset", "help"},
		"/remote-control ":  {"on", "off"},
		"/title ":            {"on", "off"},
		"/plugin ":           {"list", "install", "uninstall", "enable", "disable"},
	}
	for command, values := range rules {
		if strings.HasPrefix(prefix, command) {
			query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(prefix, command)))
			matches := make([]string, 0, len(values))
			for _, value := range values {
				if query == "" || strings.HasPrefix(value, query) {
					matches = append(matches, strings.TrimSpace(command+" "+value))
				}
			}
			return matches
		}
	}
	if !strings.HasPrefix(prefix, "/") || strings.Contains(prefix, " ") {
		return nil
	}
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
	u.history.edit()
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
	var value string
	var ok bool
	if direction < 0 {
		value, ok = u.history.up(string(u.input))
	} else {
		value, ok = u.history.down(string(u.input))
	}
	if !ok {
		return
	}
	u.input = []rune(value)
	u.cursor = len(u.input)
	u.completionActive = false
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
	for start > 0 && (u.input[start-1] == ' ' || u.input[start-1] == '\n' || u.input[start-1] == '	') {
		start--
	}
	for start > 0 && u.input[start-1] != ' ' && u.input[start-1] != '\n' && u.input[start-1] != '	' {
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
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return colors{}
	}

	c := colors{
		on: true,
		dim: "[2m", bold: "[1m",
		cyan: "[36m", green: "[32m", yellow: "[33m",
		red: "[31m", blue: "[34m", invert: "[7m", reset: "[0m",
	}
	switch strings.ToLower(strings.TrimSpace(cfg.ColorScheme)) {
	case "tokyo night":
		c.cyan = "[38;5;117m"
		c.green = "[38;5;151m"
		c.yellow = "[38;5;222m"
		c.red = "[38;5;203m"
		c.blue = "[38;5;111m"
	case "solarized dark":
		c.cyan = "[38;5;37m"
		c.green = "[38;5;64m"
		c.yellow = "[38;5;136m"
		c.red = "[38;5;124m"
		c.blue = "[38;5;33m"
	}
	return c
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
	cols, rows := u.terminalSize()
	if cols < 40 {
		cols = 40
	}
	if rows < 12 {
		rows = 12
	}

	promptRows := u.promptRows(cols)
	completionRows := u.completionRows()
	bodyRows := rows - 4 - promptRows - completionRows
	if bodyRows < 1 {
		bodyRows = 1
	}

	all := u.visualLines(cols)
	maxScroll := max(0, len(all)-bodyRows)
	if u.followBottom {
		u.scrollTop = maxScroll
	} else {
		u.scrollTop = clamp(u.scrollTop, 0, maxScroll)
	}

	start := u.scrollTop
	end := min(len(all), start+bodyRows)

	var b strings.Builder
	b.Grow((rows + 8) * cols)
	b.WriteString("\x1b[?2026h\x1b[H")

	// Header: useful identity only. Model/permission/account state lives in the
	// status line when it is relevant instead of occupying the whole top bar.
	b.WriteString("\x1b[2K" + u.col.bold + "agy-open" + u.col.reset)
	b.WriteString("  " + u.col.dim + shortPath(u.agent.WorkspaceRoot()) + u.col.reset)
	b.WriteString("\r\n")
	b.WriteString("\x1b[2K" + strings.Repeat("─", cols) + "\r\n")

	for i := start; i < end; i++ {
		b.WriteString("\x1b[2K")
		b.WriteString(u.paint(all[i].kind, all[i].text))
		b.WriteString("\r\n")
	}
	for i := end; i < start+bodyRows; i++ {
		b.WriteString("\x1b[2K\r\n")
	}

	b.WriteString("\x1b[2K" + strings.Repeat("─", cols) + "\r\n")
	hint := "Enter send · Tab complete · PgUp/PgDn or Shift+↑/↓ scroll · Ctrl+C cancel/exit · / commands"
	if u.approval != nil {
		hint = fmt.Sprintf("ALLOW: %s%s %s%s  ·  y / n / Enter",
			u.col.bold, u.approval.action, u.approval.target, u.col.reset)
	} else if u.working && u.showStatus {
		hint = fmt.Sprintf("%s%s%s  ·  model %s  ·  %s  ·  %s",
			u.col.cyan, runningText(u.status), u.col.reset,
			u.agent.Model(), permissionLabel(u.agent.ApprovalMode()), authLabel())
	} else if u.completionActive {
		hint = "↑/↓ select · Enter/Tab accept · Esc close"
	} else if maxScroll > 0 {
		hint += fmt.Sprintf(" · output %d%%", (u.scrollTop*100)/maxScroll)
	}
	b.WriteString("\x1b[2K" + u.paint("hint", clipVisible(hint, cols)) + "\r\n")

	lines := strings.Split(string(u.input), "\n")
	b.WriteString("\x1b[2K")
	b.WriteString(u.col.bold + "› " + u.col.reset)
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n  ")
		}
		b.WriteString(u.paint("prompt", clipVisible(line, max(1, cols-3))))
	}
	last := lines[len(lines)-1]
	b.WriteString(fmt.Sprintf("\x1b[%dG", len([]rune(last))+3))

	if u.completionActive {
		matches := u.completionMatches()
		b.WriteString("\r\n" + u.col.dim + "suggestions" + u.col.reset + "\r\n")
		n := min(8, len(matches))
		for i := 0; i < n; i++ {
			b.WriteString("\x1b[2K")
			prefix, style := "  ", ""
			if i == u.completionIndex {
				prefix, style = "› ", u.col.invert
			}
			b.WriteString(style + prefix + matches[i] + u.col.reset)
			if desc := commandDescription[matches[i]]; desc != "" {
				b.WriteString("  " + u.col.dim + desc + u.col.reset)
			}
			b.WriteString("\r\n")
		}
	}

	if u.overlay != nil {
		b.WriteString(u.overlayFrame(cols))
		promptRow := rows - u.promptRows(cols) - u.completionRows() + 1
		if promptRow < 1 {
			promptRow = 1
		}
		last := strings.Split(string(u.input), "\n")
		cursorCol := len([]rune(last[len(last)-1])) + 3
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", promptRow, cursorCol))
	}
	b.WriteString("\x1b[?25h\x1b[?2026l")
	_, _ = os.Stdout.Write([]byte(b.String()))
}
func (u *UI) renderPromptOnly(cols int) {
	rows := u.termRows
	if rows < 12 {
		rows = 12
	}
	if cols < 40 {
		cols = 40
	}

	promptRows := u.promptRows(cols)
	completionRows := u.completionRows()
	totalRows := promptRows + completionRows
	if totalRows < 1 {
		totalRows = 1
	}
	startRow := rows - totalRows + 1

	var b strings.Builder
	b.Grow(totalRows*max(1, cols/2) + 128)
	b.WriteString("\x1b[?2026h")
	b.WriteString(fmt.Sprintf("\x1b[%d;1H", startRow))
	for i := 0; i < totalRows; i++ {
		b.WriteString("\x1b[2K")
		if i+1 < totalRows {
			b.WriteString("\r\n")
		}
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;1H", startRow))
	u.appendPrompt(&b, cols)
	b.WriteString("\x1b[?2026l")
	_, _ = os.Stdout.Write([]byte(b.String()))
}

func (u *UI) visualLines(cols int) []message {
	sourceLen := len(u.lines)
	lastText, lastKind := "", ""
	if sourceLen > 0 {
		lastKind = u.lines[sourceLen-1].kind
		if lastKind == "agent-stream" {
			lastText = u.streamText.String()
		} else {
			lastText = u.lines[sourceLen-1].text
		}
	}
	if u.visualCacheValid &&
		u.visualCacheCols == cols &&
		u.visualCacheLines == sourceLen &&
		u.visualCacheLastText == lastText &&
		u.visualCacheLastKind == lastKind &&
		u.visualCacheWorking == u.working &&
		u.visualCacheStatus == u.status {
		return u.visualCache
	}

	if u.visualCacheValid &&
		u.visualCacheCols == cols &&
		u.visualCacheLines == sourceLen {
		u.visualCache = u.visualCache[:u.visualCacheLastStart]
		if sourceLen > 0 {
			for _, wrapped := range wrapText(lastText, cols) {
				u.visualCache = append(u.visualCache, message{kind: lastKind, text: wrapped})
			}
		}
	} else {
		u.visualCache = u.visualCache[:0]
		u.visualCacheLastStart = 0
		for i, line := range u.lines {
			if i == sourceLen-1 {
				u.visualCacheLastStart = len(u.visualCache)
			}
			for _, wrapped := range wrapText(line.text, cols) {
				u.visualCache = append(u.visualCache, message{kind: line.kind, text: wrapped})
			}
		}
	}
	if u.working {
		u.visualCache = append(u.visualCache, message{kind: "working", text: "· " + runningText(u.status)})
	}
	u.visualCacheCols = cols
	u.visualCacheLines = sourceLen
	u.visualCacheLastText = lastText
	u.visualCacheLastKind = lastKind
	u.visualCacheWorking = u.working
	u.visualCacheStatus = u.status
	u.visualCacheValid = true
	return u.visualCache
}

func (u *UI) promptRows(cols int) int {
	_ = cols
	return max(1, len(strings.Split(string(u.input), "\n")))
}

func (u *UI) completionRows() int {
	if !u.completionActive {
		return 0
	}
	matches := u.completionMatches()
	if len(matches) == 0 {
		return 0
	}
	return 2 + min(8, len(matches))
}

func (u *UI) scrollOutput(delta int) {
	cols, rows := u.terminalSize()
	if cols < 40 {
		cols = 40
	}
	bodyRows := rows - 4 - u.promptRows(cols) - u.completionRows()
	if bodyRows < 1 {
		bodyRows = 1
	}
	maxScroll := max(0, len(u.visualLines(cols))-bodyRows)
	u.scrollTop = clamp(u.scrollTop+delta, 0, maxScroll)
	u.followBottom = u.scrollTop >= maxScroll
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (u *UI) visibleRows() int {
	cols, rows := u.terminalSize()
	if cols < 40 {
		cols = 40
	}
	body := rows - 4 - u.promptRows(cols) - u.completionRows()
	return max(1, body)
}

func (u *UI) appendPrompt(b *strings.Builder, cols int) {
	lines := strings.Split(string(u.input), "\n")
	b.WriteString(u.col.bold + "› " + u.col.reset)
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n  ")
		}
		b.WriteString(u.paint("prompt", clipVisible(line, max(1, cols-3))))
	}
	last := lines[len(lines)-1]
	b.WriteString(fmt.Sprintf("\x1b[%dG", len([]rune(last))+3))

	if u.completionActive {
		matches := u.completionMatches()
		b.WriteString("\r\n" + u.col.dim + "suggestions" + u.col.reset + "\r\n")
		n := min(8, len(matches))
		for i := 0; i < n; i++ {
			b.WriteString("\x1b[2K")
			prefix, style := "  ", ""
			if i == u.completionIndex {
				prefix, style = "› ", u.col.invert
			}
			b.WriteString(style + prefix + matches[i] + u.col.reset)
			if desc := commandDescription[matches[i]]; desc != "" {
				b.WriteString("  " + u.col.dim + desc + u.col.reset)
			}
			b.WriteString("\r\n")
		}
	}
}

func (u *UI) overlayFrame(cols int) string {
	o := u.overlay
	if o == nil {
		return ""
	}

	width := min(82, cols-4)
	if width < 44 {
		width = max(1, cols-2)
	}
	visibleItems := min(len(o.items), 12)
	height := visibleItems + 5
	startRow := max(3, (u.termRows-height)/2+1)
	startCol := max(1, (cols-width)/2+1)

	var b strings.Builder
	b.Grow((height + 2) * width)
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startRow, startCol))
	b.WriteString(u.col.bold + "┌" + strings.Repeat("─", width-2) + "┐" + u.col.reset)
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startRow+1, startCol))
	title := " " + o.title + " "
	b.WriteString(u.col.bold + title + strings.Repeat("─", max(0, width-2-len([]rune(title)))) + "┐" + u.col.reset)

	start := o.index - 5
	if start < 0 {
		start = 0
	}
	if start+visibleItems > len(o.items) {
		start = max(0, len(o.items)-visibleItems)
	}
	for i := 0; i < visibleItems; i++ {
		idx := start + i
		row := startRow + 2 + i
		prefix, style := "  ", ""
		if o.kind == "models" && idx < len(o.values) && o.values[idx] == u.agent.Model() {
			prefix = "✓ "
		}
		if idx == o.index {
			prefix, style = "› ", u.col.invert
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[2K%s%s%s", row, startCol,
			style, clipVisible(prefix+o.items[idx], width-2), u.col.reset))
	}

	descRow := startRow + 2 + visibleItems
	if o.kind == "settings" {
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[2K%s%s%s", descRow, startCol,
			u.col.dim, clipVisible(settingDescription(o.index), width-2), u.col.reset))
	} else {
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[2K%s%s%s", descRow, startCol,
			u.col.dim, clipVisible(o.footer, width-2), u.col.reset))
	}

	footerRow := descRow + 1
	footer := o.footer
	if o.kind == "settings" {
		footer = "↑/↓ select · Enter change · Esc close"
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[2K%s%s%s", footerRow, startCol,
		u.col.bold, clipVisible(footer, width-2), u.col.reset))

	bottom := footerRow + 1
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s└%s┘%s",
		bottom, startCol, u.col.bold, strings.Repeat("─", width-2), u.col.reset))

	return b.String()
}

func settingDescription(index int) string {
	switch index {
	case 0:
		return "Model for requests. Enter opens the model picker."
	case 1:
		return "Reasoning budget. Cycles low → medium → high."
	case 2:
		return "Tool trust level. Controls automatic file/shell actions."
	case 3:
		return "Color palette used by the terminal UI."
	case 4:
		return "Alternate-screen behavior at startup."
	case 5:
		return "Bell after a request finishes."
	case 6:
		return "Interface information density."
	case 7:
		return "Editor used by Ctrl+G and /open."
	case 8:
		return "Show request state and account information."
	case 9:
		return "Show detailed tool activity in the transcript."
	case 10:
		return "Capture mouse for scrolling; off lets the terminal select text."
	default:
		return ""
	}
}

func (u *UI) paint(kind, text string) string {
	switch kind {
	case "user":
		return u.col.bold + u.col.blue + text + u.col.reset
	case "agent", "agent-stream":
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
				if r[i-1] == ' ' || r[i-1] == '	' {
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

func setMouseReporting(enabled bool) {
	if enabled {
		fmt.Print("\x1b[?1000h\x1b[?1006h")
		return
	}
	disableMouseReporting()
}

func disableMouseReporting() {
	fmt.Print("\x1b[?1000l\x1b[?1006l")
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
	case '	':
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
	case 19:
		return "CTRL-S", nil
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
	seq := make([]rune, 0, 64)
	for len(seq) < 64 {
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
	if strings.HasPrefix(s, "<") {
		// SGR mouse: wheel is useful for scrolling; clicks/releases must never
		// masquerade as ESC and mutate the prompt.
		fields := strings.Split(s, ";")
		if len(fields) > 0 {
			codeText := strings.TrimPrefix(fields[0], "<")
			var code int
			if _, err := fmt.Sscanf(codeText, "%d", &code); err == nil {
				switch {
				case code == 64:
					return "MOUSE-UP", nil
				case code == 65:
					return "MOUSE-DOWN", nil
				default:
					return "MOUSE-IGNORE", nil
				}
			}
		}
		return "MOUSE-IGNORE", nil
	}
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
	case "1;2A", "1;5A":
		return "PUP", nil
	case "1;2B", "1;5B":
		return "PDOWN", nil
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

func (u *UI) refreshTerminalSize() {
	cols, rows := size()
	u.termCols = cols
	u.termRows = rows
}

func (u *UI) terminalSize() (int, int) {
	if u.termCols == 0 || u.termRows == 0 {
		u.refreshTerminalSize()
	}
	return u.termCols, u.termRows
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
