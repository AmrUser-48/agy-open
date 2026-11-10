package tui

import (
	"bufio"
	"encoding/json"
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
	"unicode"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

type message struct {
	kind string
	text string
}

type approvalRequest struct {
	action string
	target string
	reply  chan tools.ApprovalDecision
}

type uiEvent struct {
	kind     string
	text     string
	tool     string
	target   string
	command  string
	callID   string
	output   string
	ok       bool
	at       time.Time
	duration time.Duration
	workerID string
	workerRole string
	workerTask string
	workerStatus string
	approval *approvalRequest
	models   []gemini.ModelOption
}

type overlay struct {
	title      string
	items      []string
	values     []string
	keys       []string
	settingKey string
	selected   string
	index      int
	kind       string
	footer     string
}

type settingEdit struct {
	key   string
	label string
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
	"/add-dir", "/agent", "/agents", "/artifact", "/boost", "/btw", "/clear", "/codesearch",
	"/config", "/context", "/conversation", "/copy", "/credits", "/diff", "/effort",
	"/exit", "/fast", "/feedback", "/fork", "/goal", "/grill-me", "/help", "/hooks",
	"/keybindings", "/learn", "/login", "/logout", "/mcp", "/model", "/new", "/open",
	"/max-turns", "/permissions", "/plan", "/planning", "/plugin", "/plugins", "/quit",
	"/remote-control", "/rename", "/resume", "/rewind", "/schedule", "/settings", "/branch",
	"/sandbox", "/skills", "/statusline", "/switch", "/tasks", "/theme", "/teamwork", "/teamwork-preview",
	"/title", "/undo", "/usage", "/quota", "/voice", "/record", "/browser",
}

var commandDescription = map[string]string{
	"/add-dir": "Add a directory to the active workspace",
	"/agent": "Select the active agent role",
	"/agents": "List available agent roles",
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
	"/effort": "Set reasoning effort: low, medium, high, xhigh",
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
	"/login": "Sign in with the active model provider",
	"/logout": "Log out of the active model provider",
	"/mcp": "Open MCP manager",
	"/model": "Select a model",
	"/max-turns": "Set the maximum agent turns (0–500; 0 disables the limit)",
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
	"/sandbox": "Enable, disable, or inspect the Bubblewrap terminal sandbox",
	"/rename": "Rename the current conversation",
	"/resume": "Open the conversation picker",
	"/rewind": "Roll back one conversation turn",
	"/schedule": "Scheduled tasks (not available in this build)",
	"/settings": "Alias for /config",
	"/skills": "Browse agent skills",
	"/statusline": "Customize status line",
	"/switch": "Alias for /resume",
	"/theme": "Select color theme (terminal, catppuccin, monokai, gruvbox, solarized, tokyo night, dracula)",
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
	killBuffer string
	verticalGoalCol int
	verticalGoalSet bool

	history    promptHistory
	scrollTop int
	followBottom bool
	undoStack    []string
	redoStack    []string

	working bool
	cancel  context.CancelFunc
	status  string
	ticker  *time.Ticker
	lastCtrlC time.Time

	completionActive bool
	completionIndex  int

	overlay     *overlay
	configEdit  *settingEdit
	approval    *approvalRequest

	events chan uiEvent
	keys   chan string
	resize chan os.Signal

	showStatus bool
	title      bool
	mouseMode  bool
	trajectory      bool
	altScreenActive bool
	exit            bool

	modelOptions []gemini.ModelOption
	modelLoading bool
	streaming    bool
	lineMode     bool
	spinnerFrame int
	spinnerVisible bool
	streamStarted bool
	streamLastByte byte
	raw          rawState
	termCols      int
	termRows      int
	streamDirty   bool
	streamMu      sync.Mutex
	streamBuf     strings.Builder
	streamText    strings.Builder
	lineOverlayRows int
	promptRowsRendered int
	promptCursorRow    int

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
		lineMode:     true,
		trajectory:   cfg.Trajectory,
	}
	a.SetConfirm(u.confirm)
	a.SetEventSink(u.agentEvent)
	sort.Strings(commandNames)
	u.restoreChatHistory()
	return u
}

func (u *UI) restoreChatHistory() {
	u.lines = nil
	for _, entry := range u.agent.ChatMessages() {
		switch entry.Kind {
		case "tool_call":
			mark, kind := "✓", "success"
			if !entry.OK {
				mark, kind = "✗", "error"
			}
			label := entry.Tool
			if entry.Target != "" && entry.Command == "" {
				label += "  " + entry.Target
			}
			if entry.DurationMS > 0 {
				label += fmt.Sprintf("  (%d ms)", entry.DurationMS)
			}
			if entry.Command != "" {
				u.lines = append(u.lines, message{"shell", "$ " + entry.Command})
			}
			u.lines = append(u.lines, message{kind, mark + " " + label})
			if entry.Arguments != "" {
				u.lines = append(u.lines, message{"dim", "  args: " + entry.Arguments})
			}
			if u.trajectory && strings.TrimSpace(entry.Output) != "" {
				u.addBlock("tool output", entry.Output, "dim")
			}
		case "tool_approval":
			label := entry.Decision + " " + entry.Tool
			if entry.Target != "" {
				label += "  " + entry.Target
			}
			u.lines = append(u.lines, message{"info", "⚑ " + label})
		case "team_worker":
			label := "Worker " + entry.WorkerID
			if entry.WorkerRole != "" {
				label += " · " + entry.WorkerRole
			}
			label += " · " + entry.WorkerStatus
			u.lines = append(u.lines, message{"info", label})
			if strings.TrimSpace(entry.WorkerTask) != "" {
				u.lines = append(u.lines, message{"dim", "  task: " + entry.WorkerTask})
			}
			if strings.TrimSpace(entry.Output) != "" {
				u.addBlock("  ↳ worker summary", entry.Output, "dim")
			}
		default:
			switch entry.Role {
			case "user":
				u.lines = append(u.lines, message{"user", entry.Content})
			case "model":
				if strings.TrimSpace(entry.Content) != "" {
					u.lines = append(u.lines, message{"agent", entry.Content})
				}
			}
		}
	}
	u.scrollTop = 0
	u.followBottom = true
	u.streamText.Reset()
	u.streamStarted = false
	u.streamDirty = false
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
	setBracketedPaste(true)
	setEnhancedKeyboard(true)
	u.refreshTerminalSize()
	u.lineMode = true
	u.applyTerminalMode()
	fmt.Print("\x1b[?25h\x1b[0m")
	u.setTitle()

	signal.Notify(u.resize, syscall.SIGWINCH)
	defer signal.Stop(u.resize)
	defer func() {
		setBracketedPaste(false)
		setEnhancedKeyboard(false)
		if u.cancel != nil {
			u.cancel()
		}
		if u.approval != nil {
			select {
			case u.approval.reply <- tools.ApprovalDeny:
			default:
			}
		}
		u.agent.SetTextSink(nil)
		u.agent.SetEventSink(nil)
		u.streamMu.Lock()
		fmt.Print("\r\x1b[2K\n\x1b[?25h\x1b[0m")
		u.streamMu.Unlock()
		disableMouseReporting()
		if u.altScreenActive {
			leaveAltScreen()
			u.altScreenActive = false
		}
		u.raw.restore()
	}()

	if prompts, err := session.RecentPrompts(200); err == nil {
		u.history = newPromptHistory(prompts)
	}

	u.termWrite(u.paint("info", "agy-open  ·  terminal agent") + "\n")
	u.termWrite(u.paint("dim", "workspace  "+u.agent.WorkspaceRoot()) + "\n")
	u.termWrite(u.paint("dim", "model      "+u.agent.Model()) + "\n")
	if u.cfg.ShowTips {
		u.termWrite(u.paint("hint", "scrollback mode · terminal owns output history · /config opens settings") + "\n")
	}
	u.printPrompt()

	go u.keyLoop()
	ticker := time.NewTicker(u.animationSpeed())
	u.ticker = ticker
	defer func() {
		ticker.Stop()
		u.ticker = nil
	}()

	for !u.exit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case key := <-u.keys:
			u.handleKey(ctx, key)
		case ev := <-u.events:
			u.handleEvent(ev)
		case <-ticker.C:
			u.animate()
		case <-u.resize:
			u.refreshTerminalSize()
			if u.lineMode {
				u.renderLinePrompt()
			} else {
				u.render()
			}
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
	if key != "UP" && key != "DOWN" && key != "CTRL-P" && key != "CTRL-N" {
		u.verticalGoalSet = false
	}
	if u.handlePastedKey(key) {
		return
	}
	if u.lineMode {
		u.handleLineKey(ctx, key)
		return
	}
	cols, _ := u.terminalSize()
	beforePromptRows := u.promptRows(cols)
	beforeCompletionRows := u.completionRows()
	beforeCompletionIndex := u.completionIndex
	beforeOverlay := u.overlay != nil
	beforeLineCount := len(u.lines)
	beforeWorking := u.working
	beforeScrollTop := u.scrollTop
	beforeFollowBottom := u.followBottom

	if key == "EOF" {
		u.exit = true
		return
	}
	if u.configEdit != nil {
		u.handleConfigEditKey(key)
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
		if !u.working {
			u.submit(ctx)
		}
	case "CTRL-J", "SHIFT-ENTER":
		u.insertNewline()
	case "TAB":
		if !u.tryAcceptCompletion() {
			u.complete()
		}
	case "UP", "CTRL-P":
		if u.completionActive {
			u.moveCompletion(-1)
		} else if !u.working && !u.moveCursorVertical(-1) {
			u.navigateHistory(-1)
		}
	case "DOWN", "CTRL-N":
		if u.completionActive {
			u.moveCompletion(1)
		} else if !u.working && !u.moveCursorVertical(1) {
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
		start, _ := currentLineBounds(u.input, u.cursor)
		u.cursor = start
		u.updateCompletion()
	case "CTRL-E", "END":
		_, end := currentLineBounds(u.input, u.cursor)
		u.cursor = end
		u.updateCompletion()
	case "BACKSPACE", "CTRL-H":
		if !u.working {
			u.history.edit()
			u.saveUndo()
			u.deleteBackward()
			u.updateCompletion()
		}
	case "CTRL-D":
		if !u.working {
			if len(u.input) == 0 {
				u.exit = true
				return
			}
			u.history.edit()
			u.saveUndo()
			u.deleteForward()
			u.updateCompletion()
		}
	case "CTRL-W":
		u.killWordBackward()
	case "CTRL-U":
		u.killToLineStart()
	case "CTRL-K":
		u.killToLineEnd()
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
	case "ALT-B":
		u.moveWordBackward()
	case "ALT-F":
		u.moveWordForward()
	case "ALT-D":
		u.killWordForward()
	case "ALT-BACKSPACE":
		u.killWordBackward()
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
		u.yank()
	case "CTRL-T":
		u.transposeCharacters()
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
		key == "CTRL-L"

	if full {
		u.render()
	} else {
		u.renderPromptOnly(cols)
	}
}

func (u *UI) handleLineKey(ctx context.Context, key string) {
	if u.configEdit != nil {
		u.handleConfigEditKey(key)
		return
	}
	if u.overlay != nil {
		u.handleLineOverlay(ctx, key)
		return
	}
	if u.approval != nil {
		u.handleApproval(key)
		return
	}
	switch key {
	case "EOF":
		u.exit = true
		return
	case "ENTER":
		if !u.working {
			u.submit(ctx)
		}
	case "CTRL-J", "SHIFT-ENTER":
		u.insertNewline()
	case "TAB":
		if !u.tryAcceptCompletion() {
			u.complete()
		}
	case "UP", "CTRL-P":
		if u.completionActive {
			u.moveCompletion(-1)
		} else if !u.working && !u.moveCursorVertical(-1) {
			u.navigateHistory(-1)
		}
	case "DOWN", "CTRL-N":
		if u.completionActive {
			u.moveCompletion(1)
		} else if !u.working && !u.moveCursorVertical(1) {
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
		start, _ := currentLineBounds(u.input, u.cursor)
		u.cursor = start
		u.updateCompletion()
	case "CTRL-E", "END":
		_, end := currentLineBounds(u.input, u.cursor)
		u.cursor = end
		u.updateCompletion()
	case "BACKSPACE", "CTRL-H":
		if !u.working {
			u.history.edit()
			u.saveUndo()
			u.deleteBackward()
			u.updateCompletion()
		}
	case "CTRL-D":
		if !u.working {
			if len(u.input) == 0 {
				u.exit = true
				return
			}
			u.history.edit()
			u.saveUndo()
			u.deleteForward()
			u.updateCompletion()
		}
	case "CTRL-W":
		u.killWordBackward()
	case "CTRL-U":
		u.killToLineStart()
	case "CTRL-K":
		u.killToLineEnd()
	case "CTRL-C":
		u.ctrlC()
	case "ESC":
		u.completionActive = false
	case "CTRL-L":
		u.termWrite("\x1b[2J\x1b[H")
	case "PUP", "PDOWN", "MOUSE-UP", "MOUSE-DOWN", "MOUSE-IGNORE":
		// Terminal scrollback owns output history and mouse selection.
	case "CTRL-S":
		// Deliberately keep mouse reporting disabled in scrollback mode.
	case "ALT-B":
		u.moveWordBackward()
	case "ALT-F":
		u.moveWordForward()
	case "ALT-D":
		u.killWordForward()
	case "ALT-BACKSPACE":
		u.killWordBackward()
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
		u.cfg.Trajectory = u.trajectory
		u.persistConfig()
	case "CTRL-R":
		if !u.working {
			u.showDiff()
		}
	case "CTRL-Y":
		u.yank()
	case "CTRL-T":
		u.transposeCharacters()
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
	if u.overlay != nil {
		if !u.working && u.lineOverlayRows == 0 {
			u.clearPrompt()
			u.renderLineOverlay()
		}
		return
	}
	if !u.exit && !u.working {
		u.renderLinePrompt()
	} else if !u.exit && u.completionActive {
		u.renderLinePrompt()
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
		u.clearSpinner()
		if u.lineMode {
			u.termWrite(u.paint("warning", "Interrupted") + "\n")
			u.printPrompt()
		} else {
			u.lines = append(u.lines, message{"warning", "Interrupted"})
		}
		if u.approval != nil {
			select {
			case u.approval.reply <- tools.ApprovalDeny:
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
		if u.lineMode {
			u.renderLinePrompt()
		} else {
			u.lines = append(u.lines, message{"hint", "Prompt cleared. Press Ctrl+C again to exit."})
		}
	} else {
		if u.lineMode {
			u.clearPrompt()
			u.termWrite(u.paint("hint", "Press Ctrl+C again to exit.") + "\n")
			u.printPrompt()
		} else {
			u.lines = append(u.lines, message{"hint", "Press Ctrl+C again to exit."})
		}
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

func (u *UI) handlePastedKey(key string) bool {
	if strings.HasPrefix(key, "PASTE:") {
		if u.overlay != nil || u.approval != nil {
			return true
		}
		u.insertPastedText(strings.TrimPrefix(key, "PASTE:"))
		if !u.working {
			if u.lineMode {
				u.renderLinePrompt()
			} else {
				u.render()
			}
		}
		return true
	}
	if strings.HasPrefix(key, "PASTE_ERROR:") {
		u.notifyMessage("error", strings.TrimPrefix(key, "PASTE_ERROR:"))
		if !u.lineMode {
			u.render()
		}
		return true
	}
	return false
}

func normalizePastedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r == 0 || (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

func (u *UI) insertPastedText(text string) {
	if u.working {
		return
	}
	text = normalizePastedText(text)
	if text == "" {
		return
	}
	if u.configEdit == nil {
		u.history.edit()
	}
	u.saveUndo()
	inserted := []rune(text)
	cursor := clamp(u.cursor, 0, len(u.input))
	u.input = append(u.input[:cursor], append(inserted, u.input[cursor:]...)...)
	u.cursor = cursor + len(inserted)
	u.completionActive = false
}

func (u *UI) startAgent(parent context.Context, prompt string) {
	u.lines = append(u.lines, message{"user", prompt})
	u.working = true
	u.streaming = true
	u.status = "Thinking"
	u.spinnerFrame = 0
	u.spinnerVisible = true
	u.streamStarted = false
	u.streamLastByte = 0
	u.clearPrompt()
	u.termWrite(u.paint("user", "› "+displayPrompt(prompt)) + "\n")
	u.printSpinner()

	runCtx, cancel := context.WithCancel(parent)
	u.cancel = cancel
	u.agent.SetTextSink(func(s string) {
		if s == "" {
			return
		}
		u.streamMu.Lock()
		defer u.streamMu.Unlock()
		if u.spinnerVisible {
			fmt.Print("\r\x1b[2K")
			u.spinnerVisible = false
		}
		if _, err := os.Stdout.Write([]byte(u.paint("agent-stream", s))); err == nil {
			u.streamStarted = true
			u.streamLastByte = s[len(s)-1]
		}
	})

	go func() {
		var out bytes.Buffer
		err := u.agent.RunPrompt(runCtx, prompt, &out)
		u.events <- uiEvent{kind: "done", text: out.String(), output: errorText(err), ok: err == nil}
	}()
}

func (u *UI) startTeamwork(parent context.Context, task string) {
	task = strings.TrimSpace(task)
	if task == "" {
		u.notifyMessage("error", "usage: /teamwork <task>")
		return
	}
	u.lines = append(u.lines, message{"user", "Teamwork: "+task})
	u.working = true
	u.streaming = false
	u.status = "Planning teamwork"
	u.spinnerFrame = 0
	u.spinnerVisible = true
	u.clearPrompt()
	u.termWrite(u.paint("user", "› /teamwork "+displayPrompt(task)) + "\n")
	u.printSpinner()

	runCtx, cancel := context.WithCancel(parent)
	u.cancel = cancel
	u.agent.SetTextSink(nil)
	go func() {
		var out bytes.Buffer
		err := u.agent.RunTeamwork(runCtx, task, &out)
		u.events <- uiEvent{kind: "done", text: out.String(), output: errorText(err), ok: err == nil}
	}()
}

func agentEventToUIEvent(e agent.Event) uiEvent {
	command := e.Command
	if e.Tool == "shell" && strings.TrimSpace(command) == "" {
		command = e.Target
	}
	return uiEvent{
		kind: e.Kind, tool: e.Tool, target: e.Target, command: command, callID: e.CallID,
		output: e.Output, ok: e.OK, at: e.At, duration: e.Duration,
		workerID: e.WorkerID, workerRole: e.WorkerRole,
		workerTask: e.WorkerTask, workerStatus: e.WorkerStatus,
	}
}

func (u *UI) agentEvent(e agent.Event) {
	u.events <- agentEventToUIEvent(e)
}

func (u *UI) confirm(action, target string) tools.ApprovalDecision {
	req := &approvalRequest{action: action, target: target, reply: make(chan tools.ApprovalDecision, 1)}
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
	if u.lineMode {
		u.streamMu.Lock()
		defer u.streamMu.Unlock()
		if u.spinnerVisible {
			fmt.Print("\r\x1b[2K")
			u.spinnerVisible = false
		}
		_, _ = os.Stdout.Write([]byte(text))
		u.streamStarted = true
		u.streamLastByte = text[len(text)-1]
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
	if u.lineMode {
		u.handleLineEvent(ev)
		return
	}
	switch ev.kind {
	case "text_delta":
		if ev.text != "" {
			u.appendStreamText(ev.text)
		}
	case "tool_start":
		u.status = "Running " + toolDisplayName(ev.tool)
		u.lines = append(u.lines, message{"tool", u.toolStartLine(ev)})
	case "tool_end":
		u.status = "Thinking"
		kind, line := u.toolEndLine(ev)
		u.lines = append(u.lines, message{kind, line})
		if u.trajectory && strings.TrimSpace(ev.output) != "" {
			u.addBlock("  ↳ output", u.formatToolOutput(ev.output), "dim")
		}
	case "worker_start":
		u.status = "Worker " + ev.workerID + " · " + ev.workerRole
		u.lines = append(u.lines, message{"tool", u.workerStartLine(ev)})
	case "worker_end":
		u.status = "Thinking"
		kind, line := u.workerEndLine(ev)
		u.lines = append(u.lines, message{kind, line})
		if strings.TrimSpace(ev.output) != "" {
			u.addBlock("  ↳ worker summary", u.formatToolOutput(ev.output), "dim")
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
		if u.lineMode {
			u.clearPrompt()
			u.renderLineOverlay()
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
		if ev.command != "" {
			u.lines = append(u.lines, message{"shell", "$ " + sanitizeTerminalText(ev.command, false)})
		}
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

func (u *UI) handleLineEvent(ev uiEvent) {
	switch ev.kind {
	case "tool_start":
		u.clearSpinner()
		u.status = "Running " + toolDisplayName(ev.tool)
		u.termWrite(u.paint("tool", u.toolStartLine(ev)) + "\n")
		u.printSpinner()
	case "tool_end":
		u.clearSpinner()
		u.status = "Thinking"
		kind, line := u.toolEndLine(ev)
		u.termWrite(u.paint(kind, line) + "\n")
		if u.trajectory && strings.TrimSpace(ev.output) != "" {
			u.termWrite(u.paint("dim", "  ↳ output") + "\n")
			u.termWrite(u.paint("dim", u.formatToolOutput(ev.output)) + "\n")
		}
		if u.working && u.approval == nil {
			u.printSpinner()
		}
	case "worker_start":
		u.clearSpinner()
		u.status = "Worker " + ev.workerID + " · " + ev.workerRole
		u.termWrite(u.paint("tool", u.workerStartLine(ev)) + "\n")
		u.printSpinner()
	case "worker_end":
		u.clearSpinner()
		u.status = "Thinking"
		kind, line := u.workerEndLine(ev)
		u.termWrite(u.paint(kind, line) + "\n")
		if strings.TrimSpace(ev.output) != "" {
			u.termWrite(u.paint("dim", "  ↳ worker summary") + "\n")
			u.termWrite(u.paint("dim", u.formatToolOutput(ev.output)) + "\n")
		}
		if u.working && u.approval == nil {
			u.printSpinner()
		}
	case "approval":
		u.clearSpinner()
		u.approval = ev.approval
		command := strings.TrimSpace(ev.approval.target)
		if ev.approval.action == "shell" && tools.RequiresLifecycleApproval(command) {
			u.termWrite("\n" + u.col.yellow + "One-time confirmation required for system action:\n   " + command + "\n\n" +
				"Run once?\n  1. Yes, run once\n  2. No, cancel\n" + u.col.reset + "> ")
		} else {
			targetLabel := command
			if ev.approval.action != "shell" {
				targetLabel = ev.approval.action + ": " + command
			}
			u.termWrite("\n" + u.col.yellow + "Approval required for:\n   " + targetLabel + "\n\n" +
				"Allow this operation?\n" +
				"  1. Allow once\n" +
				"  a. Allow remaining operations for this response\n" +
				"  d. Deny remaining operations for this response\n" +
				"  4. Deny once\n" + u.col.reset + "> ")
		}
	case "models":
		u.modelLoading = false
		u.status = ""
		if !ev.ok || len(ev.models) == 0 {
			u.termWrite(u.paint("error", ev.text) + "\n")
			u.printPrompt()
			return
		}
		u.modelOptions = ev.models
		items := make([]string, 0, len(ev.models))
		values := make([]string, 0, len(ev.models))
		for _, model := range ev.models {
			label := model.Label()
			if len(model.SupportedEfforts) > 0 {
				label += "  [" + strings.Join(model.SupportedEfforts, "/") + "]"
			}
			items = append(items, label)
			values = append(values, model.ID)
		}
		u.overlay = &overlay{
			title: "Models",
			items: items,
			values: values,
			kind: "models",
			footer: "↑/↓ select · Enter apply · Esc close",
		}
		u.clearPrompt()
		u.renderLineOverlay()
	case "shell_done":
		u.clearSpinner()
		u.working = false
		u.cancel = nil
		u.status = ""
		if ev.command != "" {
			u.termWrite(u.paint("shell", "$ "+sanitizeTerminalText(ev.command, false)) + "\n")
		}
		if ev.ok && strings.TrimSpace(ev.text) != "" {
			u.termWrite(u.col.yellow + ev.text + u.col.reset)
			if !strings.HasSuffix(ev.text, "\n") {
				u.termWrite("\n")
			}
		} else if !ev.ok && ev.output != "" {
			u.termWrite(u.paint("error", ev.output) + "\n")
		}
		u.printPrompt()
	case "message":
		u.clearSpinner()
		kind := "info"
		if !ev.ok {
			kind = "error"
		}
		u.termWrite(u.paint(kind, ev.text) + "\n")
		if !u.working {
			u.printPrompt()
		}
	case "done":
		u.agent.SetTextSink(nil)
		u.clearSpinner()
		u.working = false
		u.streaming = false
		u.cancel = nil
		u.status = ""
		u.streamMu.Lock()
		if u.streamStarted {
			if u.streamLastByte != '\n' {
				_, _ = os.Stdout.Write([]byte("\n"))
			}
		} else if ev.ok && strings.TrimSpace(ev.text) != "" {
			_, _ = os.Stdout.Write([]byte(u.paint("agent-stream", strings.TrimSpace(ev.text)) + "\n"))
		} else if !ev.ok && ev.output != "" {
			_, _ = os.Stdout.Write([]byte(u.paint("error", ev.output) + "\n"))
		}
		u.streamMu.Unlock()
		if u.cfg.Notifications {
			fmt.Print("\a")
		}
		u.printPrompt()
	}
}

func (u *UI) termWrite(s string) {
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	_, _ = os.Stdout.Write([]byte(s))
}

func displayPrompt(s string) string {
	return sanitizeTerminalText(s, true)
}

func (u *UI) clearPrompt() {
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	u.erasePromptLocked()
}

func (u *UI) erasePromptLocked() {
	rows := u.promptRowsRendered
	if rows <= 0 {
		fmt.Print("\r\x1b[2K")
		return
	}
	cursorRow := clamp(u.promptCursorRow, 0, rows-1)
	if down := rows - cursorRow - 1; down > 0 {
		fmt.Printf("\x1b[%dB", down)
	}
	fmt.Print("\r")
	if rows > 1 {
		fmt.Printf("\x1b[%dA", rows-1)
	}
	for i := 0; i < rows; i++ {
		fmt.Print("\r\x1b[2K")
		if i+1 < rows {
			fmt.Print("\x1b[1B")
		}
	}
	if rows > 1 {
		fmt.Printf("\x1b[%dA", rows-1)
	}
	fmt.Print("\r")
	u.promptRowsRendered = 0
	u.promptCursorRow = 0
}

func (u *UI) paintPromptLine(line string) string {
	leading := len(line) - len(strings.TrimLeft(line, " \t"))
	content := line[leading:]
	end := strings.IndexAny(content, " \t")
	command := content
	if end >= 0 {
		command = content[:end]
	}
	for _, name := range commandNames {
		if command == name {
			return u.col.cyan + line[:leading] + u.col.green + command +
				u.col.cyan + content[len(command):] + u.col.reset
		}
	}
	return u.paint("prompt", line)
}

func (u *UI) printPrompt() {
	u.spinnerVisible = false
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	u.erasePromptLocked()

	cols, _ := u.terminalSize()
	layout := layoutPrompt(u.input, u.cursor, cols)
	for i, line := range layout.lines {
		if i > 0 {
			fmt.Print("\r\n")
		}
		fmt.Print("\x1b[2K")
		prefix := "  "
		if i == 0 {
			prefix = "› "
		}
		fmt.Print(u.col.bold + u.col.cyan + prefix + u.col.reset + u.paintPromptLine(line))
	}

	suggestionRows := 0
	if u.completionActive {
		matches := u.completionMatches()
		maxSuggestionRows := max(0, u.termRows-len(layout.lines)-2)
		if len(matches) > 0 && maxSuggestionRows >= 2 {
			fmt.Print("\r\n" + u.col.dim + "commands" + u.col.reset)
			suggestionRows = 1
			visible := min(8, min(len(matches), maxSuggestionRows-1))
			for i, match := range matches[:visible] {
				prefix, style := "  ", ""
				if i == u.completionIndex {
					prefix, style = "› ", u.col.invert
				}
				line := prefix + match
				if desc := commandDescription[match]; desc != "" {
					line += "  " + desc
				}
				fmt.Print("\r\n\x1b[2K" + style + clipVisible(line, max(1, cols-2)) + u.col.reset)
				suggestionRows++
			}
		}
	}

	u.promptRowsRendered = len(layout.lines) + suggestionRows
	u.promptCursorRow = layout.cursorRow
	if down := u.promptRowsRendered - layout.cursorRow - 1; down > 0 {
		fmt.Printf("\x1b[%dA", down)
	}
	fmt.Printf("\r\x1b[%dC", max(0, layout.cursorCol-1))
}

func (u *UI) renderLinePrompt() {
	if u.working {
		return
	}
	u.printPrompt()
}

func (u *UI) clearSpinner() {
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	if u.spinnerVisible {
		fmt.Print("\r\x1b[2K")
		u.spinnerVisible = false
	}
}

func (u *UI) printSpinner() {
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	if !u.working || !u.cfg.ShowStatus || u.streamStarted || u.approval != nil {
		return
	}
	status := u.status
	width := u.termCols - 4
	if width < 24 {
		width = 72
	}
	status = clipVisible(status, width)
	fmt.Print("\r\x1b[2K" + u.col.cyan + spinnerFrame(u.spinnerFrame) + " " + status + "…" + u.col.reset)
	u.spinnerVisible = true
}

func (u *UI) animate() {
	if !u.working || u.streamStarted || u.approval != nil {
		return
	}
	u.spinnerFrame++
	u.printSpinner()
}

func (u *UI) handleApproval(key string) {
	if u.approval == nil {
		return
	}
	decision := tools.ApprovalDeny
	selected := false
	status := ""
	responseDecision := false
	if u.approval.action == "shell" && tools.RequiresLifecycleApproval(u.approval.target) {
		switch key {
		case "RUNE:1":
			decision, selected = tools.ApprovalOnce, true
		case "RUNE:2", "RUNE:4", "RUNE:n", "RUNE:N", "ESC":
			decision, selected = tools.ApprovalDeny, true
		}
	} else {
		switch key {
		case "RUNE:1":
			decision, selected = tools.ApprovalOnce, true
		case "RUNE:a", "RUNE:A":
			u.agent.AllowCurrentResponse()
			decision, selected, responseDecision = tools.ApprovalOnce, true, true
		case "RUNE:d", "RUNE:D":
			u.agent.DenyCurrentResponse()
			decision, selected, responseDecision = tools.ApprovalDeny, true, true
		case "RUNE:2", "RUNE:3":
			decision, selected = tools.ApprovalDeny, true
		case "RUNE:4", "RUNE:n", "RUNE:N", "ESC":
			decision, selected = tools.ApprovalDeny, true
		}
	}
	if !selected {
		return
	}
	select {
	case u.approval.reply <- decision:
	default:
	}
	messageText, kind := "Denied", "warning"
	if responseDecision && decision == tools.ApprovalOnce {
		messageText, kind = "Allowed remaining operations for this response", "success"
	} else if responseDecision {
		messageText, kind = "Denied remaining operations for this response", "warning"
	}
	switch decision {
	case tools.ApprovalOnce:
		if !responseDecision {
			messageText, kind = "Approved once", "success"
		}
	case tools.ApprovalForSession:
		messageText, kind = "Allowed for this conversation", "success"
	case tools.ApprovalPersistent:
		messageText, kind = "Allowed and saved to settings.json", "success"
	}
	if status != "" {
		messageText = status
		kind = "error"
	}
	u.approval = nil
	if u.lineMode {
		u.streamMu.Lock()
		fmt.Print("\r\x1b[2K")
		fmt.Print(u.paint(kind, messageText) + "\n")
		u.streamMu.Unlock()
		if u.working {
			u.status = "Thinking"
			u.spinnerVisible = false
			u.printSpinner()
		} else {
			u.printPrompt()
		}
		return
	}
	u.lines = append(u.lines, message{kind, messageText})
	u.render()
}

func (u *UI) notifyMessage(kind, text string) {
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.paint(kind, text) + "\n")
		u.printPrompt()
	} else {
		u.lines = append(u.lines, message{kind, text})
	}
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
		if u.lineMode { u.printHelpLine() } else { u.openHelp() }
	case "/exit", "/quit":
		u.exit = true
	case "/clear", "/new":
		u.agent.Clear()
		u.lines = nil
	case "/agent":
		if arg == "" {
			u.fetchAgents()
		} else if err := u.agent.SetRole(arg); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.cfg = u.agent.Config()
			u.notifyMessage("success", "Active role: "+u.agent.RoleName())
		}
	case "/model":
		if arg == "" {
			u.fetchModels(ctx)
		} else if err := u.selectModelArg(arg); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		}
	case "/effort":
		if arg == "" {
			u.notifyMessage("info", "Current effort: "+u.agent.Effort())
		} else if !u.supportsEffort(arg) {
			u.notifyMessage("error", "Effort "+strings.ToLower(strings.TrimSpace(arg))+" is not supported by "+u.agent.Model())
		} else if err := u.agent.SetEffort(arg); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.cfg.Effort = u.agent.Effort()
			u.persistConfig()
			u.notifyMessage("success", "Effort: "+u.agent.Effort())
		}
	case "/max-turns":
		if arg == "" {
			u.notifyMessage("info", fmt.Sprintf("Max turns: %d (range 0–500; 0 disables the limit)", u.cfg.MaxTurns))
			break
		}
		turns, err := strconv.Atoi(arg)
		if err != nil {
			u.notifyMessage("error", "usage: /max-turns <0-500>")
			break
		}
		if err := u.agent.SetMaxTurns(turns); err != nil {
			u.notifyMessage("error", err.Error())
			break
		}
		u.cfg.MaxTurns = turns
		u.persistConfig()
		u.notifyMessage("success", fmt.Sprintf("Max turns: %d", turns))
	case "/config", "/settings":
		u.openSettings()
		if u.lineMode {
			u.clearPrompt()
			u.renderLineOverlay()
		}
	case "/resume", "/switch", "/conversation":
		if u.lineMode {
			u.printResumeLine()
		} else if arg == "" {
			u.openResume()
		} else if err := u.agent.ResumeSession(arg); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.restoreChatHistory()
			u.notifyMessage("success", "Resumed "+arg)
		}
	case "/rewind", "/undo":
		if err := u.agent.Rewind(); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.notifyMessage("success", "Conversation rewound")
		}
	case "/diff", "/artifact":
		u.showDiff()
	case "/copy":
		u.copyLast()
	case "/open":
		if arg == "" {
			u.notifyMessage("error", "usage: /open <path>")
		} else if !u.working {
			u.openPath(arg)
		}
	case "/login":
		if u.lineMode {
			u.clearPrompt()
		}
		if err := u.agent.Login(ctx); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.notifyMessage("success", "Signed in to "+u.cfg.ModelProvider)
		}
	case "/logout":
		if err := u.agent.Logout(); err != nil {
			u.notifyMessage("error", err.Error())
		} else {
			u.notifyMessage("success", "Logged out of "+u.cfg.ModelProvider)
		}
	case "/sandbox":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "on", "enable", "enabled":
			if err := u.agent.SetTerminalSandbox(true); err != nil {
				u.notifyMessage("error", "Could not enable terminal sandbox: "+err.Error())
			} else {
				u.cfg = u.agent.Config()
				u.notifyMessage("success", "Terminal sandbox enabled (Bubblewrap; shell commands only)")
			}
		case "off", "disable", "disabled":
			if err := u.agent.SetTerminalSandbox(false); err != nil {
				u.notifyMessage("error", "Could not disable terminal sandbox: "+err.Error())
			} else {
				u.cfg = u.agent.Config()
				u.notifyMessage("success", "Terminal sandbox disabled")
			}
		case "", "status":
			state := "disabled"
			if u.cfg.EnableTerminalSandbox { state = "enabled" }
			u.notifyMessage("info", "Terminal sandbox: "+state+" · usage: /sandbox on|off")
		default:
			u.notifyMessage("error", "usage: /sandbox [on|off|status]")
		}
	case "/permissions":
		if arg != "" {
			switch strings.ToLower(strings.TrimSpace(arg)) {
			case "disable", "read-only", "workspace-write", "ask", "autopilot":
				_ = u.agent.SetApproval(arg)
				u.cfg.ApprovalMode = u.agent.ApprovalMode()
				u.persistConfig()
			default:
				u.notifyMessage("error", "usage: /permissions [disable|read-only|workspace-write|ask|autopilot]")
			}
			return
		}
		u.overlay = &overlay{
			title: "Permissions",
			items: []string{"disable", "read-only", "workspace-write", "ask", "autopilot"},
			kind: "permissions",
			footer: "Enter apply · Esc close",
			index: permissionIndex(u.agent.ApprovalMode()),
		}
		if u.lineMode {
			u.clearPrompt()
			u.renderLineOverlay()
		}
	case "/ask":
		_ = u.agent.SetApproval("ask")
		u.cfg.ApprovalMode = u.agent.ApprovalMode()
		u.persistConfig()
	case "/approve":
		_ = u.agent.SetApproval("autopilot")
		u.cfg.ApprovalMode = u.agent.ApprovalMode()
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
			u.notifyMessage("info", "Custom statusline commands are not configured in agy-open; restored the built-in status line.")
		case "help":
			u.notifyMessage("info", "/statusline [on|off|enable|disable|delete|reset|help]")
		default:
			u.notifyMessage("warning", "Custom statusline commands are not implemented; use /statusline on or off.")
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
				u.notifyMessage("warning", "Plugin command accepted, but the plugin manager is not implemented in this lightweight build.")
			default:
				u.notifyMessage("error", "usage: /plugin [install|uninstall|enable|disable|list] [name]")
			}
		}
	case "/skills":
		u.openDirectory(".agents/skills", "Skills")
	case "/tasks":
		u.notifyMessage("info", "Foreground tool activity is shown above.")
	case "/usage", "/quota", "/credits":
		u.notifyMessage("info", "Usage is provided by the Google account service; model availability is shown by /model.")
	case "/feedback":
		u.notifyMessage("info", "Feedback: https://github.com/AmrUser-48/agy-open/issues")
	case "/rename":
		if arg == "" {
			u.notifyMessage("error", "usage: /rename <name>")
		} else {
			u.notifyMessage("success", "Conversation title: "+arg)
		}
	case "/fork", "/branch":
		u.agent.Clear()
		u.notifyMessage("success", "Started a new conversation context")
	case "/teamwork", "/teamwork-preview":
		u.startTeamwork(ctx, arg)
	case "/boost", "/btw":
		if arg == "" {
			u.notifyMessage("error", "usage: "+cmd+" <task>")
		} else {
			u.startAgent(ctx, arg)
		}
	case "/add-dir":
		u.notifyMessage("warning", "Additional roots are not yet supported by the workspace security boundary.")
	case "/context":
		u.overlay = &overlay{
			title: "Context",
			items: []string{
				fmt.Sprintf("Approximate context characters: %d", u.agent.ContextChars()),
				"Conversation: " + u.agent.SessionID(),
				"Role: " + u.agent.RoleName(),
				"Model: " + u.agent.Model(),
			},
			kind: "info",
			footer: "Esc close",
		}
		if u.lineMode {
			u.clearPrompt()
			u.renderLineOverlay()
		}
	case "/remote-control":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "", "status":
			u.notifyMessage("info", "Remote control is not configured in this standalone build.")
		case "on", "off":
			u.notifyMessage("warning", "Remote control is not available in agy-open.")
		default:
			u.notifyMessage("error", "usage: /remote-control [on|off]")
		}
	case "/voice", "/record":
		u.notifyMessage("warning", "Voice input is unavailable in agy-open.")
	case "/plan":
		if arg == "" {
			u.notifyMessage("error", "usage: /plan <task>")
		} else {
			_ = u.agent.SetEffort("high")
			u.cfg.Effort = "high"
			u.persistConfig()
			u.startAgent(ctx, "Plan the following task before making changes: "+arg)
		}
	case "/goal":
		if arg == "" {
			u.notifyMessage("error", "usage: /goal <task>")
		} else {
			_ = u.agent.SetApproval("autopilot")
			u.cfg.ApprovalMode = u.agent.ApprovalMode()
			u.persistConfig()
			u.startAgent(ctx, "Work continuously toward this goal and verify the result: "+arg)
		}
	case "/grill-me":
		if arg == "" {
			u.notifyMessage("error", "usage: /grill-me <task>")
		} else {
			u.startAgent(ctx, "Interview me about requirements, trade-offs, and edge cases before implementing: "+arg)
		}
	case "/learn":
		if arg == "" {
			u.notifyMessage("error", "usage: /learn <observation>")
		} else {
			u.startAgent(ctx, "Analyze this session correction and propose a persistent project rule or skill: "+arg)
		}
	case "/codesearch":
		if arg == "" {
			u.notifyMessage("error", "usage: /codesearch <query>")
		} else {
			u.startAgent(ctx, "Search this workspace for: "+arg)
		}
	case "/theme":
		if arg == "" {
			schemes := append([]string(nil), tuiColorSchemes...)
			items := make([]string, 0, len(schemes))
			current, _ := normalizeColorScheme(u.cfg.ColorScheme)
			for _, scheme := range schemes {
				if scheme == current {
					items = append(items, "✓ "+scheme)
				} else {
					items = append(items, "  "+scheme)
				}
			}
			u.overlay = &overlay{
				title:  "TUI color schemes",
				items:  items,
				values: schemes,
				kind:   "theme",
				footer: "↑/↓ select · Enter apply · Esc close",
			}
			if u.lineMode {
				u.clearPrompt()
				u.renderLineOverlay()
			}
		} else {
			if err := u.applyColorScheme(arg); err != nil {
				u.notifyMessage("error", err.Error())
			} else {
				u.notifyMessage("success", "TUI color scheme: "+u.cfg.ColorScheme)
			}
		}
		case "/browser", "/schedule":
		u.lines = append(u.lines, message{"warning", cmd+" is not available in this lightweight build."})
	default:
		u.lines = append(u.lines, message{"error", "Unknown command: " + cmd})
	}
}

func permissionIndex(mode string) int {
	switch tools.NormalizeApprovalMode(mode) {
	case tools.ModeDisabled:
		return 0
	case tools.ModeReadOnly:
		return 1
	case tools.ModeWorkspace:
		return 2
	case tools.ModeAsk:
		return 3
	default:
		return 4
	}
}

func (u *UI) supportsEffort(level string) bool {
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "low" && level != "medium" && level != "high" && level != "xhigh" {
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
	next := levels[0]
	for i, level := range levels {
		if strings.EqualFold(level, current) {
			next = levels[(i+1)%len(levels)]
			break
		}
	}
	if err := u.agent.SetEffort(next); err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	u.cfg = u.agent.Config()
	u.notifyMessage("success", "Reasoning effort: "+u.agent.Effort())
}

func (u *UI) cyclePermission() {
	modes := []string{"disable", "read-only", "workspace-write", "ask", "autopilot"}
	i := (permissionIndex(u.agent.ApprovalMode()) + 1) % len(modes)
	if err := u.agent.SetApproval(modes[i]); err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
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
		"Ctrl+A/Home        start of current line",
		"Ctrl+E/End         end of current line",
		"Ctrl+B/Ctrl+F      move left/right",
		"Alt+B/Alt+F        move one word backward/forward",
		"Ctrl+W/Alt+Backspace delete previous word",
		"Alt+D              delete next word",
		"Ctrl+U/Ctrl+K      kill to line start/end",
		"Ctrl+Y             yank last killed text",
		"Ctrl+T             transpose adjacent characters",
		"Ctrl+G             external editor",
		"Ctrl+V             paste",
		"Ctrl+O             tool trajectory",
		"Ctrl+R             git diff",
		"Ctrl+S             toggle mouse mode (off = terminal selection)",
		"Shift+Tab          permission mode",
		"Alt+Z/Alt+Y        undo/redo",
		"Tab                autocomplete",
		"↑/↓/Ctrl+P/N       multiline cursor or history",
		"PgUp/PgDn          scroll output",
	}
	u.overlay = &overlay{title: "Keybindings", items: items, kind: "info", footer: "Esc close"}
}

func (u *UI) openSettings() {
	fields := config.Settings()
	items := make([]string, 0, len(fields))
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		value, err := config.SettingValue(u.cfg, field.Key)
		if err != nil {
			value = "unavailable"
		}
		items = append(items, fmt.Sprintf("%-27s %s", field.Label, settingValueLabel(field, value)))
		keys = append(keys, field.Key)
	}
	u.overlay = &overlay{
		title:  "Configuration",
		items:  items,
		keys:   keys,
		kind:   "settings",
		footer: "↑/↓ select · Enter edit · Esc close",
	}
}

func settingValueLabel(field config.Setting, value string) string {
	switch field.Kind {
	case "bool":
		return onOff(value == "true")
	case "text-multiline":
		value = strings.ReplaceAll(value, "\n", " ↵ ")
		if value == "" {
			return "(empty)"
		}
	case "approval-rules":
		var rules []config.ApprovalRule
		if json.Unmarshal([]byte(value), &rules) == nil {
			if len(rules) == 0 {
				return "(none)"
			}
			return fmt.Sprintf("%d command prefixes", len(rules))
		}
	}
	if len([]rune(value)) > 52 {
		return string([]rune(value)[:49]) + "…"
	}
	return value
}

func (u *UI) openSettingChoices(field config.Setting) {
	current, _ := config.SettingValue(u.cfg, field.Key)
	index := 0
	for i, value := range field.Options {
		if value == current {
			index = i
			break
		}
	}
	u.overlay = &overlay{
		title:      field.Label,
		items:      append([]string(nil), field.Options...),
		values:     append([]string(nil), field.Options...),
		settingKey: field.Key,
		selected:   current,
		kind:       "config-choice",
		index:      index,
		footer:     "↑/↓ select · Enter apply · Esc back",
	}
}

func effectiveSpinnerSpeed(speed string) string {
	if speed == "" {
		return "medium"
	}
	return speed
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
		u.openSettings()
	case "permissions":
		modes := []string{"disable", "enable", "ask", "autopilot"}
		_ = u.agent.SetApproval(modes[o.index])
		u.cfg.ApprovalMode = u.agent.ApprovalMode()
		u.persistConfig()
	case "theme":
		if o.index < len(o.values) {
			u.overlay = nil
			if err := u.applyColorScheme(o.values[o.index]); err != nil {
				u.lines = append(u.lines, message{"error", err.Error()})
			} else {
				u.lines = append(u.lines, message{"success", "TUI color scheme: " + u.cfg.ColorScheme})
			}
		}
		case "resume":
		id := o.items[o.index]
		if o.index < len(o.values) && strings.TrimSpace(o.values[o.index]) != "" {
			id = o.values[o.index]
		}
		if err := u.agent.ResumeSession(id); err != nil {
			u.lines = append(u.lines, message{"error", err.Error()})
		} else {
			u.restoreChatHistory()
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
	case "config-choice":
		if o.index < len(o.values) {
			key, value := o.settingKey, o.values[o.index]
			u.overlay = nil
			if err := u.applyConfigSetting(key, value); err != nil {
				u.notifyMessage("error", err.Error())
			}
			u.openSettings()
		}
	}
}

func (u *UI) changeSetting(index int, ctx context.Context) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.keys) {
		return
	}
	key := u.overlay.keys[index]
	var field config.Setting
	found := false
	for _, candidate := range config.Settings() {
		if candidate.Key == key {
			field = candidate
			found = true
			break
		}
	}
	if !found {
		return
	}

	switch field.Kind {
	case "bool":
		current, err := config.SettingValue(u.cfg, field.Key)
		if err != nil {
			u.notifyMessage("error", err.Error())
			return
		}
		next := "true"
		if current == "true" {
			next = "false"
		}
		if err := u.applyConfigSetting(field.Key, next); err != nil {
			u.notifyMessage("error", err.Error())
			return
		}
		u.openSettings()
	case "enum":
		u.openSettingChoices(field)
	case "model":
		u.overlay = nil
		u.fetchModels(ctx)
	case "number", "text", "text-multiline", "approval-rules":
		u.beginSettingEdit(field)
	default:
		u.notifyMessage("error", "unsupported setting editor: "+field.Kind)
		return
	}
}

func (u *UI) applyConfigSetting(key, value string) error {
	next := u.cfg
	next.SafetySettings = append([]config.SafetySetting(nil), u.cfg.SafetySettings...)
	if err := config.SetSetting(&next, key, value); err != nil {
		return err
	}
	if err := u.agent.UpdateConfig(next); err != nil {
		return err
	}
	u.cfg = u.agent.Config()
	u.showStatus = u.cfg.ShowStatus
	u.trajectory = u.cfg.Trajectory
	u.mouseMode = u.cfg.MouseMode
	u.col = newColors(u.cfg)
	u.applyTerminalMode()
	if u.ticker != nil {
		u.ticker.Reset(u.animationSpeed())
	}
	if u.mouseMode && !u.lineMode {
		setMouseReporting(true)
	} else {
		setMouseReporting(false)
	}
	return nil
}

func (u *UI) beginSettingEdit(field config.Setting) {
	value, err := config.SettingValue(u.cfg, field.Key)
	if err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	if u.lineMode {
		u.eraseLineOverlay()
	}
	u.overlay = nil
	u.configEdit = &settingEdit{key: field.Key, label: field.Label}
	u.input = []rune(value)
	u.cursor = len(u.input)
	u.completionActive = false
	u.termWrite("\n" + u.col.bold + "Edit " + field.Label + u.col.reset + "\n")
	u.termWrite(u.col.dim + "Enter save · Shift+Enter newline · Esc cancel · Ctrl+V paste" + u.col.reset + "\n")
	u.renderLinePrompt()
}

func (u *UI) finishSettingEdit(save bool) {
	edit := u.configEdit
	if edit == nil {
		return
	}
	value := string(u.input)
	u.configEdit = nil
	u.input = nil
	u.cursor = 0
	u.completionActive = false

	if save {
		if err := u.applyConfigSetting(edit.key, value); err != nil {
			u.termWrite(u.paint("error", err.Error()) + "\n")
			u.openSettings()
			u.renderLineOverlay()
			return
		}
		u.termWrite(u.paint("success", edit.label+" saved") + "\n")
	} else {
		u.termWrite(u.paint("dim", edit.label+" unchanged") + "\n")
	}
	u.openSettings()
	u.renderLineOverlay()
}

func (u *UI) handleConfigEditKey(key string) {
	if u.configEdit == nil {
		return
	}
	switch key {
	case "ENTER":
		u.finishSettingEdit(true)
		return
	case "SHIFT-ENTER", "CTRL-J":
		u.input = append(u.input[:u.cursor], append([]rune{'\n'}, u.input[u.cursor:]...)...)
		u.cursor++
	case "ESC", "CTRL-C":
		u.finishSettingEdit(false)
		return
	case "LEFT", "CTRL-B":
		if u.cursor > 0 { u.cursor-- }
	case "RIGHT", "CTRL-F":
		if u.cursor < len(u.input) { u.cursor++ }
	case "UP", "CTRL-P":
		u.cursor = moveTextCursorVertical(u.input, u.cursor, -1)
	case "DOWN", "CTRL-N":
		u.cursor = moveTextCursorVertical(u.input, u.cursor, 1)
	case "HOME", "CTRL-A":
		start, _ := currentLineBounds(u.input, u.cursor)
		u.cursor = start
	case "END", "CTRL-E":
		_, end := currentLineBounds(u.input, u.cursor)
		u.cursor = end
	case "BACKSPACE", "CTRL-H":
		u.deleteBackward()
	case "CTRL-D":
		u.deleteForward()
	case "CTRL-W":
		u.killWordBackward()
	case "CTRL-U":
		u.killToLineStart()
	case "CTRL-K":
		u.killToLineEnd()
	case "ALT-B":
		u.moveWordBackward()
	case "ALT-F":
		u.moveWordForward()
	case "ALT-D":
		u.killWordForward()
	case "ALT-BACKSPACE":
		u.killWordBackward()
	case "CTRL-Y":
		u.yank()
	case "CTRL-T":
		u.transposeCharacters()
	case "CTRL-V":
		u.paste()
	default:
		if strings.HasPrefix(key, "RUNE:") {
			r := []rune(strings.TrimPrefix(key, "RUNE:"))
			if len(r) > 0 {
				u.input = append(u.input[:u.cursor], append(r, u.input[u.cursor:]...)...)
				u.cursor += len(r)
			}
		}
	}
	u.renderLinePrompt()
}

func moveTextCursorVertical(input []rune, cursor, direction int) int {
	cursor = clamp(cursor, 0, len(input))
	lineStart := cursor
	for lineStart > 0 && input[lineStart-1] != '\n' {
		lineStart--
	}
	column := cursor - lineStart
	if direction < 0 {
		if lineStart == 0 { return cursor }
		prevEnd := lineStart - 1
		prevStart := prevEnd
		for prevStart > 0 && input[prevStart-1] != '\n' {
			prevStart--
		}
		return prevStart + min(column, prevEnd-prevStart)
	}
	lineEnd := cursor
	for lineEnd < len(input) && input[lineEnd] != '\n' {
		lineEnd++
	}
	if lineEnd == len(input) { return cursor }
	nextStart := lineEnd + 1
	nextEnd := nextStart
	for nextEnd < len(input) && input[nextEnd] != '\n' {
		nextEnd++
	}
	return nextStart + min(column, nextEnd-nextStart)
}

func (u *UI) printHelpLine() {
	u.clearPrompt()
	u.termWrite(u.col.bold + "Commands" + u.col.reset + "\n")
	names := append([]string(nil), commandNames...)
	for _, name := range names {
		desc := commandDescription[name]
		if desc == "" {
			desc = "Compatibility command"
		}
		u.termWrite(fmt.Sprintf("  %-18s %s\n", name, desc))
	}
	u.printPrompt()
}

func (u *UI) eraseLineOverlay() {
	if u.lineOverlayRows <= 0 {
		return
	}
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	fmt.Printf("\x1b[%dA", u.lineOverlayRows)
	for i := 0; i < u.lineOverlayRows; i++ {
		fmt.Print("\r\x1b[2K")
		if i+1 < u.lineOverlayRows {
			fmt.Print("\x1b[1B")
		}
	}
	fmt.Printf("\x1b[%dA", max(0, u.lineOverlayRows-1))
	u.lineOverlayRows = 0
}

func (u *UI) renderLineOverlay() {
	o := u.overlay
	if o == nil {
		return
	}
	u.refreshTerminalSize()
	visible := min(len(o.items), 12)
	if visible < 1 { visible = 1 }
	maxVisible := max(1, u.termRows-6)
	if visible > maxVisible { visible = maxVisible }
	start := 0
	if o.index >= visible { start = o.index-visible+1 }
	if start+visible > len(o.items) { start = max(0, len(o.items)-visible) }
	var b strings.Builder
	b.WriteString(u.col.bold + o.title + u.col.reset + "\n")
	for i := 0; i < visible; i++ {
		idx := start+i
		prefix, style := "  ", ""
		if idx == o.index { prefix, style = "› ", u.col.invert }
		if o.kind == "models" && idx < len(o.values) && strings.EqualFold(o.values[idx], u.agent.Model()) {
			if idx == o.index { prefix = "›✓ " } else { prefix = "✓ " }
		}
		b.WriteString(style + prefix + clipVisible(o.items[idx], max(1, u.termCols-6)) + u.col.reset + "\n")
	}
	if o.kind == "config-choice" && o.index < len(o.values) {
		b.WriteString(u.col.dim + clipVisible("Current: "+o.selected+" · "+o.footer, max(1, u.termCols-2)) + u.col.reset + "\n")
	} else {
		b.WriteString(u.col.dim + clipVisible(o.footer, max(1, u.termCols-2)) + u.col.reset + "\n")
	}
	u.streamMu.Lock()
	defer u.streamMu.Unlock()
	_, _ = os.Stdout.Write([]byte(b.String()))
	u.lineOverlayRows = visible+2
}

func (u *UI) handleLineOverlay(ctx context.Context, key string) {
	o := u.overlay
	if o == nil { return }
	switch key {
	case "ESC", "RUNE:q", "RUNE:Q", "CTRL-C":
		u.eraseLineOverlay(); u.overlay = nil; u.printPrompt()
	case "UP", "RUNE:k":
		if o.index > 0 { u.eraseLineOverlay(); o.index--; u.renderLineOverlay() }
	case "DOWN", "RUNE:j":
		if o.index+1 < len(o.items) { u.eraseLineOverlay(); o.index++; u.renderLineOverlay() }
	case "PUP", "CTRL-U":
		u.eraseLineOverlay(); o.index -= max(1, u.visibleRows()/2); if o.index < 0 { o.index = 0 }; u.renderLineOverlay()
	case "PDOWN", "CTRL-D":
		u.eraseLineOverlay(); o.index += max(1, u.visibleRows()/2); if o.index >= len(o.items) { o.index = len(o.items)-1 }; u.renderLineOverlay()
	case "HOME", "RUNE:g":
		u.eraseLineOverlay(); o.index = 0; u.renderLineOverlay()
	case "END", "RUNE:G":
		u.eraseLineOverlay(); o.index = max(0, len(o.items)-1); u.renderLineOverlay()
	case "ENTER":
		u.eraseLineOverlay(); u.changeLineOverlaySelection(ctx)
	}
}

func (u *UI) changeLineOverlaySelection(ctx context.Context) {
	o := u.overlay
	if o == nil { return }
	switch o.kind {
	case "models":
		if o.index >= len(o.values) { u.overlay=nil; u.printPrompt(); return }
		value := o.values[o.index]
		u.overlay = nil
		u.selectModel(value)
		u.openSettings()
		u.renderLineOverlay()
	case "settings":
		u.changeSetting(o.index, ctx)
		if u.overlay != nil { u.renderLineOverlay() } else { u.printPrompt() }
	case "config-choice":
		if o.index < len(o.values) {
			key, value := o.settingKey, o.values[o.index]
			u.overlay = nil
			if err := u.applyConfigSetting(key, value); err != nil {
				u.termWrite(u.paint("error", err.Error()) + "\n")
			}
			u.openSettings()
			u.renderLineOverlay()
		}
	case "permissions":
		modes := []string{"disable", "enable", "ask", "autopilot"}
		if o.index < len(modes) { _ = u.agent.SetApproval(modes[o.index]); u.cfg.ApprovalMode = u.agent.ApprovalMode(); u.persistConfig() }
		u.openSettings(); u.renderLineOverlay()
	case "theme":
		if o.index < len(o.values) {
			u.overlay = nil
			if err := u.applyColorScheme(o.values[o.index]); err != nil {
				u.notifyMessage("error", err.Error())
			} else {
				u.notifyMessage("success", "TUI color scheme: "+u.cfg.ColorScheme)
			}
		}
		case "resume":
		if o.index >= len(o.values) { u.overlay=nil; u.printPrompt(); return }
		id := o.values[o.index]
		if err := u.agent.ResumeSession(id); err != nil { u.overlay=nil; u.termWrite(u.paint("error", err.Error())+"\n"); u.printPrompt(); return }
		u.restoreChatHistory()
		u.overlay=nil; u.termWrite(u.paint("success", "Resumed "+id)+"\n"); u.printPrompt()
	}
}
func (u *UI) printResumeLine() {
	entries, err := u.agent.SessionList(24)
	u.clearPrompt()
	if err != nil {
		u.termWrite(u.paint("error", err.Error()) + "\n")
		u.printPrompt()
		return
	}
	if len(entries) == 0 {
		u.termWrite("No saved conversations\n")
		u.printPrompt()
		return
	}
	u.termWrite(u.col.bold + "Conversations" + u.col.reset + "\n")
	for _, entry := range entries {
		u.termWrite(fmt.Sprintf("  %s  ·  %s\n", entry.ID, entry.ModTime.Local().Format("2006-01-02 15:04")))
	}
	u.termWrite(u.col.dim + "Use /resume <id> to continue one." + u.col.reset + "\n")
	u.printPrompt()
}

func (u *UI) fetchModels(ctx context.Context) {
	if u.modelLoading {
		return
	}
	u.modelLoading = true
	u.status = "Loading models"
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.col.dim + "Loading current model catalog…" + u.col.reset + "\n")
	}
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
	roles, err := agent.ListRoles()
	if err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	items := make([]string, 0, len(roles))
	for _, role := range roles {
		marker := "  "
		if role.Name == u.agent.RoleName() {
			marker = "* "
		}
		items = append(items, fmt.Sprintf("%s%-12s %s", marker, role.Name, role.Description))
	}
	u.overlay = &overlay{
		title: "Agent roles",
		items: items,
		kind: "info",
		footer: "Use /agent <name> to switch · Esc close",
	}
	if u.lineMode {
		u.clearPrompt()
		u.renderLineOverlay()
	}
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
	if err := u.agent.SetModelOption(model); err != nil {
		u.lines = append(u.lines, message{"error", err.Error()})
		return
	}
	u.cfg = u.agent.Config()
	u.persistConfig()
	u.setTitle()
	msg := "Model: " + u.agent.Model() + " · effort " + u.agent.Effort()
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.paint("success", msg) + "\n")
		u.printPrompt()
	} else {
		u.lines = append(u.lines, message{"success", msg})
	}
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
	u.cfg = u.agent.Config()
	u.persistConfig()
	u.setTitle()
	msg := "Model: " + u.agent.Model()
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.paint("success", msg) + "\n")
		u.printPrompt()
	} else {
		u.lines = append(u.lines, message{"success", msg})
	}
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
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.col.bold + "git diff" + u.col.reset + "\n")
		u.termWrite(u.paint("diff", s) + "\n")
		u.printPrompt()
	} else {
		u.addBlock("git diff", s, "diff")
	}
}

func (u *UI) startShell(ctx context.Context, command string) {
	if strings.TrimSpace(command) == "" {
		return
	}
	if u.lineMode {
		u.clearPrompt()
		u.termWrite(u.col.yellow + "$ " + command + u.col.reset + "\n")
		u.working = true
		u.status = "Waiting for approval"
	} else {
		u.lines = append(u.lines, message{"shell", "$ "+command})
		u.working = true
		u.status = "Waiting for approval"
	}
	runCtx, cancel := context.WithCancel(ctx)
	u.cancel = cancel
	go func() {
		result := u.agent.RunShell(runCtx, command)
		ev := uiEvent{kind: "shell_done", command: command, text: result.Output, ok: result.OK}
		if !result.OK {
			ev.output = result.Output
		}
		u.events <- ev
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
	if u.lineMode {
		u.clearPrompt()
	}
	u.raw.restore()
	fmt.Print("\r\x1b[2K\x1b[?25h\x1b[0m")
	if u.altScreenActive {
		leaveAltScreen()
	}
	cmd := exec.Command(program, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()

	if u.altScreenActive {
		enterAltScreen()
	}
	fmt.Print("\x1b[?25h\x1b[0m")
	next, rawErr := rawMode()
	if rawErr == nil {
		u.raw = next
	} else {
		if u.lineMode {
			u.termWrite(u.paint("error", rawErr.Error())+"\n")
		} else {
			u.lines = append(u.lines, message{"error", rawErr.Error()})
		}
	}
	if err != nil {
		if u.lineMode {
			u.termWrite(u.paint("error", program+": "+err.Error())+"\n")
		} else {
			u.lines = append(u.lines, message{"error", program+": "+err.Error()})
		}
	}
	if editPath != "" {
		defer os.Remove(editPath)
		if b, readErr := os.ReadFile(editPath); readErr == nil {
			u.input = []rune(strings.TrimSuffix(string(b), "\n"))
			u.cursor = len(u.input)
		}
	}
	if u.lineMode {
		u.printPrompt()
	} else {
		u.render()
	}
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
		u.insertPastedText(string(b))
		return
	}
	u.notifyMessage("warning", "Clipboard utility not found")
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
		commandsWithArgs := []string{"/model ", "/effort ", "/permissions ", "/statusline ", "/remote-control ", "/title ", "/plugin ", "/theme "}
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
		"/permissions ":     {"disable", "enable", "ask", "autopilot"},
		"/statusline ":      {"on", "off", "enable", "disable", "delete", "reset", "help"},
		"/remote-control ":  {"on", "off"},
		"/title ":            {"on", "off"},
		"/plugin ":           {"list", "install", "uninstall", "enable", "disable"},
		"/theme ":           {"terminal", "catppuccin", "monokai", "gruvbox", "solarized", "tokyo-night", "dracula", "dark", "lcd7s"},
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
	u.updateCompletion()
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
	u.updateCompletion()
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

func (u *UI) addBlock(title, text, kind string) {
	if title != "" {
		u.lines = append(u.lines, message{kind, title})
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		u.lines = append(u.lines, message{kind, line})
	}
}

func (u *UI) persistConfig() {
	if err := u.agent.UpdateConfig(u.cfg); err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	u.cfg = u.agent.Config()
	u.showStatus = u.cfg.ShowStatus
	u.trajectory = u.cfg.Trajectory
	u.mouseMode = u.cfg.MouseMode
	u.col = newColors(u.cfg)
	u.applyTerminalMode()
	if u.ticker != nil {
		u.ticker.Reset(u.animationSpeed())
	}
}

func (u *UI) setTitle() {
	if !u.title {
		fmt.Print("\x1b]0;\x07")
		return
	}
	fmt.Printf("\x1b]0;agy-open · %s · %s\x07", u.agent.Model(), filepath.Base(u.agent.WorkspaceRoot()))
}

func editorName(cfg config.Config) string {
	if strings.EqualFold(cfg.EditorMode, "advanced") {
		if visual := os.Getenv("VISUAL"); visual != "" {
			return visual
		}
	}
	if cfg.Editor != "" && cfg.Editor != "auto" {
		return cfg.Editor
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

func colorOutputEnabled() bool {
	force := strings.TrimSpace(os.Getenv("FORCE_COLOR"))
	if force != "" && force != "0" {
		return true
	}
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func newColors(cfg config.Config) colors {
	if !colorOutputEnabled() {
		return colors{}
	}

	c := colors{
		on: true,
		dim: "\x1b[2m", bold: "\x1b[1m",
		cyan: "\x1b[36m", green: "\x1b[32m", yellow: "\x1b[33m",
		red: "\x1b[31m", blue: "\x1b[34m", invert: "\x1b[7m", reset: "\x1b[0m",
	}
	switch strings.ToLower(strings.TrimSpace(cfg.ColorScheme)) {
	case "tokyo-night", "tokyo night":
		c.cyan = "\x1b[38;5;117m"
		c.green = "\x1b[38;5;151m"
		c.yellow = "\x1b[38;5;222m"
		c.red = "\x1b[38;5;203m"
		c.blue = "\x1b[38;5;111m"
	case "solarized", "solarized dark":
		c.cyan = "\x1b[38;5;37m"
		c.green = "\x1b[38;5;64m"
		c.yellow = "\x1b[38;5;136m"
		c.red = "\x1b[38;5;124m"
		c.blue = "\x1b[38;5;33m"
	case "monokai":
		c.cyan = "\x1b[38;5;81m"
		c.green = "\x1b[38;5;148m"
		c.yellow = "\x1b[38;5;186m"
		c.red = "\x1b[38;5;197m"
		c.blue = "\x1b[38;5;141m"
	case "gruvbox":
		c.cyan = "\x1b[38;5;109m"
		c.green = "\x1b[38;5;142m"
		c.yellow = "\x1b[38;5;214m"
		c.red = "\x1b[38;5;167m"
		c.blue = "\x1b[38;5;108m"
	case "dracula":
		c.cyan = "\x1b[38;5;117m"
		c.green = "\x1b[38;5;84m"
		c.yellow = "\x1b[38;5;228m"
		c.red = "\x1b[38;5;203m"
		c.blue = "\x1b[38;5;141m"
	case "catppuccin":
		c.cyan = "\x1b[38;5;117m"
		c.green = "\x1b[38;5;121m"
		c.yellow = "\x1b[38;5;222m"
		c.red = "\x1b[38;5;210m"
		c.blue = "\x1b[38;5;111m"
	case "dark":
		c.cyan = "\x1b[38;5;110m"
		c.green = "\x1b[38;5;108m"
		c.yellow = "\x1b[38;5;179m"
		c.red = "\x1b[38;5;167m"
		c.blue = "\x1b[38;5;110m"
	case "lcd7s":
		c.cyan = "\x1b[38;5;37m"
		c.green = "\x1b[38;5;64m"
		c.yellow = "\x1b[38;5;136m"
		c.red = "\x1b[38;5;124m"
		c.blue = "\x1b[38;5;33m"
	}
	return c
}


func wantsAltScreen(lineMode bool, mode, term string) bool {
	if lineMode {
		return false
	}
	return mode == "always" || (mode == "auto" && term != "" && term != "dumb")
}

func (u *UI) applyTerminalMode() {
	wantAlt := wantsAltScreen(u.lineMode, u.cfg.AltScreenMode, os.Getenv("TERM"))
	if wantAlt != u.altScreenActive {
		if wantAlt {
			enterAltScreen()
		} else {
			leaveAltScreen()
		}
		u.altScreenActive = wantAlt
	}
	if u.lineMode {
		setMouseReporting(false)
	} else {
		setMouseReporting(u.mouseMode)
	}
}

func (u *UI) formatToolOutput(output string) string {
	output = strings.TrimSpace(sanitizeTerminalText(output, false))
	if output == "" || u.cfg.Verbosity == "high" {
		return output
	}
	limit := 8
	if u.cfg.Verbosity == "low" {
		limit = 3
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= limit {
		return output
	}
	visible := strings.Join(lines[:limit], "\n")
	return fmt.Sprintf("%s\n… %d more lines hidden (%s verbosity)", visible, len(lines)-limit, u.cfg.Verbosity)
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
	if u.lineMode {
		u.renderLinePrompt()
		return
	}
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
		hint = fmt.Sprintf("ALLOW: %s%s%s  ·  1 once / 2 session / 3 persistent / 4 cancel",
			u.col.bold, u.approval.action, u.col.reset)
	} else if u.working && u.showStatus {
		hint = fmt.Sprintf("%s%s%s  ·  model %s  ·  %s  ·  %s",
			u.col.cyan, runningText(u.status), u.col.reset,
			u.agent.Model(), permissionLabel(u.agent.ApprovalMode()), authLabel(u.cfg.ModelProvider))
	} else if u.completionActive {
		hint = "↑/↓ select · Enter/Tab accept · Esc close"
	} else if maxScroll > 0 {
		hint += fmt.Sprintf(" · output %d%%", (u.scrollTop*100)/maxScroll)
	}
	b.WriteString("\x1b[2K" + u.paint("hint", clipVisible(hint, cols)) + "\r\n")

	layout := layoutPrompt(u.input, u.cursor, cols)
	b.WriteString("\x1b[2K")
	for i, line := range layout.lines {
		if i > 0 {
			b.WriteString("\r\n  ")
		} else {
			b.WriteString(u.col.bold + u.col.cyan + "› " + u.col.reset)
		}
		b.WriteString(u.paintPromptLine(line))
	}

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

	promptStartRow := rows - promptRows - completionRows + 1
	layoutCursor := layoutPrompt(u.input, u.cursor, cols)
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", promptStartRow+layoutCursor.cursorRow, layoutCursor.cursorCol))

	if u.overlay != nil {
		b.WriteString(u.overlayFrame(cols))
		promptRow := rows - u.promptRows(cols) - u.completionRows() + 1
		if promptRow < 1 {
			promptRow = 1
		}
		layout := layoutPrompt(u.input, u.cursor, cols)
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", promptRow+layout.cursorRow, layout.cursorCol))
	}
	b.WriteString("\x1b[?25h\x1b[?2026l")
	_, _ = os.Stdout.Write([]byte(b.String()))
}
func (u *UI) renderPromptOnly(cols int) {
	if u.lineMode {
		u.renderLinePrompt()
		return
	}
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
	layout := layoutPrompt(u.input, u.cursor, cols)
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startRow+layout.cursorRow, layout.cursorCol))
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
	return max(1, len(layoutPrompt(u.input, u.cursor, cols).lines))
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
	layout := layoutPrompt(u.input, u.cursor, cols)
	b.WriteString(u.col.bold + u.col.cyan + "› " + u.col.reset)
	for i, line := range layout.lines {
		if i > 0 {
			b.WriteString("\r\n  ")
		}
		b.WriteString(u.paintPromptLine(line))
	}

	if u.completionActive {
		matches := u.completionMatches()
		if len(matches) > 0 {
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
		description := ""
		if o.index >= 0 && o.index < len(o.keys) {
			for _, field := range config.Settings() {
				if field.Key == o.keys[o.index] { description = field.Description; break }
			}
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[2K%s%s%s", descRow, startCol,
			u.col.dim, clipVisible(description, width-2), u.col.reset))
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
	fields := config.Settings()
	if index < 0 || index >= len(fields) {
		return ""
	}
	return fields[index].Description
}

func (u *UI) paint(kind, text string) string {
	switch kind {
	case "info":
		return u.col.blue + text + u.col.reset
	case "prompt":
		return u.col.cyan + text + u.col.reset
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
	switch tools.NormalizeApprovalMode(mode) {
	case tools.ModeDisabled:
		return "disable"
	case tools.ModeReadOnly:
		return "read-only"
	case tools.ModeWorkspace:
		return "workspace-write"
	case tools.ModeAsk:
		return "ask"
	default:
		return "autopilot"
	}
}

func authLabel(provider string) string {
	if provider == "openai-compatible" {
		return "local OpenAI-compatible model server"
	}
	if provider == "openai" {
		manager := &auth.OpenAIManager{}
		if !manager.Authenticated() {
			return "not signed in to ChatGPT"
		}
		if email := manager.Email(); email != "" {
			return "ChatGPT · " + email
		}
		return "ChatGPT account"
	}
	if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
		return "Gemini API key"
	}
	if strings.TrimSpace(os.Getenv("AGY_OAUTH_REFRESH_TOKEN")) != "" {
		return "Google account (refresh token)"
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
	cmd = exec.Command("stty", "-icanon", "-echo", "-isig", "-ixon", "-icrnl", "min", "1", "time", "0")
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
	case '\r':
		return "ENTER", nil
	case '\n':
		return "CTRL-J", nil
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
	case 14:
		return "CTRL-N", nil
	case 15:
		return "CTRL-O", nil
	case 16:
		return "CTRL-P", nil
	case 18:
		return "CTRL-R", nil
	case 19:
		return "CTRL-S", nil
	case 20:
		return "CTRL-T", nil
	case 21:
		return "CTRL-U", nil
	case 22:
		return "CTRL-V", nil
	case 23:
		return "CTRL-W", nil
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

func setBracketedPaste(enabled bool) {
	if enabled {
		fmt.Print("\x1b[?2004h")
	} else {
		fmt.Print("\x1b[?2004l")
	}
}

// Enable Kitty's disambiguated key events and xterm's modified-key reporting.
// Terminals without either protocol ignore the corresponding mode sequences.
func setEnhancedKeyboard(enabled bool) {
	if enabled {
		fmt.Print("\x1b[>1u\x1b[>4;2m")
	} else {
		fmt.Print("\x1b[<u\x1b[>4;0m")
	}
}

const maxBracketedPasteBytes = 8 << 20

func readBracketedPaste(r *bufio.Reader) (string, error) {
	var paste bytes.Buffer
	end := []byte("[201~")
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", fmt.Errorf("read bracketed paste: %w", err)
		}
		if b == 0x1b {
			var sequence [5]byte
			if _, err := io.ReadFull(r, sequence[:]); err != nil {
				return "", fmt.Errorf("read bracketed paste terminator: %w", err)
			}
			if bytes.Equal(sequence[:], end) {
				return normalizePastedText(paste.String()), nil
			}
			paste.WriteByte(b)
			paste.Write(sequence[:])
		} else {
			paste.WriteByte(b)
		}
		if paste.Len() > maxBracketedPasteBytes {
			return "", fmt.Errorf("pasted text exceeds %d bytes", maxBracketedPasteBytes)
		}
	}
}

func decodeKittyKey(sequence string) (string, bool) {
	body := strings.TrimSuffix(sequence, "u")
	parts := strings.Split(body, ";")
	if len(parts) == 0 {
		return "", false
	}
	code, err := strconv.Atoi(strings.Split(parts[0], ":")[0])
	if err != nil {
		return "", false
	}
	modifiers := 1
	if len(parts) > 1 {
		modifiers, err = strconv.Atoi(strings.Split(parts[1], ":")[0])
		if err != nil {
			return "", false
		}
	}
	return decodeModifiedKey(code, modifiers)
}

func decodeModifyOtherKeys(sequence string) (string, bool) {
	if !strings.HasPrefix(sequence, "27;") || !strings.HasSuffix(sequence, "~") {
		return "", false
	}
	parts := strings.Split(strings.TrimSuffix(sequence, "~"), ";")
	if len(parts) != 3 {
		return "", false
	}
	modifiers, err1 := strconv.Atoi(parts[1])
	code, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return "", false
	}
	return decodeModifiedKey(code, modifiers)
}

func decodeModifiedCursorKey(sequence string) (string, bool) {
	if sequence == "" {
		return "", false
	}
	final := sequence[len(sequence)-1]
	if !strings.ContainsRune("ABCDHF", rune(final)) {
		return "", false
	}
	params := sequence[:len(sequence)-1]
	fields := strings.Split(params, ";")
	first := ""
	if len(fields) > 0 {
		first = strings.SplitN(fields[0], ":", 2)[0]
	}
	if first == "" {
		first = "1"
	}
	if first != "1" || len(fields) > 2 {
		return "", false
	}
	modifier := 1
	if len(fields) == 2 {
		value, err := strconv.Atoi(strings.SplitN(fields[1], ":", 2)[0])
		if err != nil || value < 1 {
			return "", false
		}
		modifier = value
	}
	// Alt/Ctrl+Left and Right retain the existing word-navigation bindings.
	if modifier == 3 || modifier == 5 {
		switch final {
		case 'C':
			return "ALT-F", true
		case 'D':
			return "ALT-B", true
		}
	}
	switch final {
	case 'A':
		return "UP", true
	case 'B':
		return "DOWN", true
	case 'C':
		return "RIGHT", true
	case 'D':
		return "LEFT", true
	case 'H':
		return "HOME", true
	case 'F':
		return "END", true
	}
	return "", false
}

func decodeModifiedKey(code, modifiers int) (string, bool) {
	if modifiers < 1 {
		modifiers = 1
	}
	mods := modifiers - 1
	shift, alt, ctrl := mods&1 != 0, mods&2 != 0, mods&4 != 0
	switch code {
	case 13:
		if shift {
			return "SHIFT-ENTER", true
		}
		if ctrl {
			return "CTRL-J", true
		}
		return "ENTER", true
	case 9:
		if shift {
			return "SHIFT-TAB", true
		}
		return "TAB", true
	case 8, 127:
		if alt {
			return "ALT-BACKSPACE", true
		}
		return "BACKSPACE", true
	case 57352:
		return "UP", true
	case 57353:
		return "DOWN", true
	case 57354:
		return "RIGHT", true
	case 57355:
		return "LEFT", true
	case 57356:
		return "HOME", true
	case 57357:
		return "END", true
	case 57358:
		return "PUP", true
	case 57359:
		return "PDOWN", true
	}
	if code < 0 || code > unicode.MaxRune {
		return "", false
	}
	key := rune(code)
	if ctrl && unicode.IsLetter(key) {
		return "CTRL-" + strings.ToUpper(string(key)), true
	}
	if alt {
		return "ALT-" + strings.ToUpper(string(key)), true
	}
	if shift {
		key = shiftedRune(key)
	}
	if unicode.IsPrint(key) {
		return "RUNE:" + string(key), true
	}
	return "", false
}

func shiftedRune(r rune) rune {
	if unicode.IsLetter(r) {
		return unicode.ToUpper(r)
	}
	const unshifted = "`" + "1234567890-=[]\\;',./"
	const shifted = "~!@#$%^&*()_+{}|:\"<>?"
	for i, ch := range unshifted {
		if ch == r {
			return []rune(shifted)[i]
		}
	}
	return r
}

func readEscape(r *bufio.Reader) (string, error) {
	ch, _, err := r.ReadRune()
	if err != nil {
		return "ESC", nil
	}
	if ch == '\r' || ch == '\n' {
		return "SHIFT-ENTER", nil
	}
	switch ch {
	case 'b', 'B':
		return "ALT-B", nil
	case 'f', 'F':
		return "ALT-F", nil
	case 'd', 'D':
		return "ALT-D", nil
	case 8, 127:
		return "ALT-BACKSPACE", nil
	case 'z', 'Z':
		return "ALT-Z", nil
	case 'y', 'Y':
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
	if key, ok := decodeModifiedCursorKey(s); ok {
		return key, nil
	}
	if strings.HasSuffix(s, "u") {
		if key, ok := decodeKittyKey(s); ok {
			return key, nil
		}
	}
	if key, ok := decodeModifyOtherKeys(s); ok {
		return key, nil
	}
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
	case "200~":
		paste, err := readBracketedPaste(r)
		if err != nil {
			return "PASTE_ERROR:" + err.Error(), nil
		}
		return "PASTE:" + paste, nil
	case "A":
		return "UP", nil
	case "B":
		return "DOWN", nil
	case "C":
		return "RIGHT", nil
	case "D":
		return "LEFT", nil
	case "H", "1~", "7~":
		return "HOME", nil
	case "F", "4~", "8~":
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
