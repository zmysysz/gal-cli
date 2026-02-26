package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/gal-cli/gal-cli/internal/agent"
	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/gal-cli/gal-cli/internal/engine"
	"github.com/gal-cli/gal-cli/internal/provider"
	"github.com/gal-cli/gal-cli/internal/session"
	"github.com/gal-cli/gal-cli/internal/tool"
	"github.com/spf13/cobra"
)

func init() {
	var agentName string
	var modelName string
	var debug bool
	var sessionID string
	var message string
	chatCmd := &cobra.Command{
		Use:   "chat",
		Short: "Start chat (interactive or non-interactive with -m)",
		Long: `Start an interactive chat session or run in non-interactive mode.

Interactive Mode:
  gal-cli chat                    # start with default agent
  gal-cli chat -a coder           # start with specific agent
  gal-cli chat --session abc123   # resume session

Non-Interactive Mode (with -m flag):
  gal-cli chat -m "your message"
  gal-cli chat -m @prompt.txt
  echo "test" | gal-cli chat -m -
  gal-cli chat --session abc -m "continue"
  gal-cli chat -a coder -m "write code" > output.txt

Output: stdout = LLM response, stderr = tool calls (use 2>/dev/null to suppress)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(agentName, modelName, sessionID, message, debug)
		},
	}
	chatCmd.Flags().StringVarP(&agentName, "agent", "a", "", "Agent name (default: from config)")
	chatCmd.Flags().StringVar(&modelName, "model", "", "Model to use (overrides agent default)")
	chatCmd.Flags().StringVar(&sessionID, "session", "", "Session ID to resume or create")
	chatCmd.Flags().StringVarP(&message, "message", "m", "", "Non-interactive mode: message to send (use @file or - for stdin)")
	chatCmd.Flags().BoolVar(&debug, "debug", false, "")
	chatCmd.Flags().MarkHidden("debug")
	rootCmd.AddCommand(chatCmd)
}

var (
	sInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	sErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	sTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	sPrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	sFaint   = lipgloss.NewStyle().Faint(true)
	sHint    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sHintSel = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	sBar     = lipgloss.NewStyle().Faint(true)
	sLogo    = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	sDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func banner(agentName, modelName, sessionID string) string {
	logo := sLogo.Render(`
   ██████╗  █████╗ ██╗      █████╗ ██╗  ██╗██╗   ██╗
  ██╔════╝ ██╔══██╗██║     ██╔══██╗╚██╗██╔╝╚██╗ ██╔╝
  ██║  ███╗███████║██║     ███████║ ╚███╔╝  ╚████╔╝
  ██║   ██║██╔══██║██║     ██╔══██║ ██╔██╗   ╚██╔╝
  ╚██████╔╝██║  ██║███████╗██║  ██║██╔╝ ██╗   ██║
   ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝`)

	info := sInfo.Render(fmt.Sprintf("  Agent: %s │ Model: %s │ Session: %s", agentName, modelName, sessionID))
	hints := sDim.Render("  /help commands │ /quit exit │ ↑↓ history │ Tab complete")

	return logo + "\n\n" + info + "\n" + hints
}

type streamChunkMsg string
type streamToolMsg string
type streamToolResultMsg string
type streamDoneMsg struct{ content string }
type streamErrMsg struct{ err error }
type compressStartMsg struct{}
type compressDoneMsg struct{}
type compressErrMsg struct{ err error }

// --- input history persistence ---

func historyPath() string {
	return filepath.Join(config.GalDir(), "history")
}

func loadHistory() []string {
	f, err := os.Open(historyPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	// keep last 500
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	return lines
}

func saveHistory(hist []string) {
	// keep last 500
	if len(hist) > 500 {
		hist = hist[len(hist)-500:]
	}
	f, err := os.Create(historyPath())
	if err != nil {
		return
	}
	defer f.Close()
	for _, line := range hist {
		fmt.Fprintln(f, line)
	}
}

// --- completions ---

var slashCommands = []string{"/agent", "/model", "/skill", "/mcp", "/shell", "/chat", "/clear", "/help", "/quit", "/exit"}

func (m *model) completions() []string {
	val := m.input.Value()
	
	// shell mode completions
	if m.shellMode && !strings.HasPrefix(val, "/") {
		return m.shellCompletions()
	}
	
	// slash command completions
	if !strings.HasPrefix(val, "/") {
		return nil
	}
	parts := strings.Fields(val)
	if len(parts) == 1 && !strings.HasSuffix(val, " ") {
		prefix := parts[0]
		var out []string
		for _, c := range slashCommands {
			if strings.HasPrefix(c, prefix) && c != prefix {
				out = append(out, c)
			}
		}
		return out
	}
	if len(parts) >= 1 {
		cmd := parts[0]
		arg := ""
		if len(parts) >= 2 {
			arg = parts[1]
		}
		var cands []string
		switch cmd {
		case "/agent":
			cands = append(cands, "list")
			if names, err := config.ListAgents(); err == nil {
				cands = append(cands, names...)
			}
		case "/model":
			cands = append(cands, "list")
			cands = append(cands, m.eng.Agent.Conf.Models...)
		case "/shell":
			cands = append(cands, "--context")
		}
		if len(cands) == 0 {
			return nil
		}
		if arg == "" {
			return cands
		}
		var out []string
		for _, c := range cands {
			if strings.HasPrefix(c, arg) && c != arg {
				out = append(out, c)
			}
		}
		return out
	}
	return nil
}

func (m *model) applyCompletion() {
	comps := m.completions()
	if len(comps) == 0 {
		return
	}
	sel := comps[m.compIdx%len(comps)]
	val := m.input.Value()
	parts := strings.Fields(val)
	if len(parts) == 1 && !strings.HasSuffix(val, " ") {
		m.input.SetValue(sel + " ")
	} else {
		m.input.SetValue(parts[0] + " " + sel)
	}
	m.input.CursorEnd()
	m.compIdx = 0
}

// --- model ---

type model struct {
	eng      *engine.Engine
	cfg      *config.Config
	reg      *tool.Registry
	sess     *session.Session
	input    textinput.Model
	spinner  spinner.Model
	renderer *glamour.TermRenderer
	width    int
	waiting  bool
	compIdx  int
	// input history
	inputHist []string
	histIdx   int
	histBuf   string
	// streaming
	streaming    string
	streamCh     chan tea.Msg
	lastStreamLn string // last partial line printed during streaming
	compressing  bool
	// shell mode
	shellMode        bool
	shellCwd         string
	shellWithContext bool // whether to add shell output to LLM context
}

func initialModel(eng *engine.Engine, cfg *config.Config, reg *tool.Registry, sess *session.Session) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 0
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	ti.Cursor.TextStyle = lipgloss.NewStyle()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))

	cwd, _ := os.Getwd()
	m := model{
		eng: eng, cfg: cfg, reg: reg, sess: sess,
		input: ti, spinner: sp, renderer: r,
		histIdx: -1, inputHist: loadHistory(),
		shellCwd: cwd,
	}
	return m
}

// printAbove returns a tea.Cmd that prints a line above the managed View area.
func printAbove(s string) tea.Cmd {
	return tea.Println(s)
}

func (m *model) statusBar() string {
	if m.waiting {
		return m.spinner.View() + sFaint.Render(" thinking...")
	}
	if m.compressing {
		return m.spinner.View() + sFaint.Render(" compressing context...")
	}
	if comps := m.completions(); len(comps) > 0 {
		var hints []string
		for i, c := range comps {
			if i == m.compIdx%len(comps) {
				hints = append(hints, sHintSel.Render(c))
			} else {
				hints = append(hints, sHint.Render(c))
			}
		}
		return sHint.Render("Tab: ") + strings.Join(hints, sHint.Render("  "))
	}
	if m.shellMode {
		modeLabel := "[Shell Mode]"
		if m.shellWithContext {
			modeLabel = "[Shell+Context]"
		}
		return sTool.Render(modeLabel+" ") + sFaint.Render(m.shellCwd)
	}
	return sBar.Render(fmt.Sprintf("%s │ %s", m.eng.Agent.Conf.Name, m.eng.Agent.CurrentModel))
}

func setIBeamCursor() tea.Msg {
	// \033[6 q = steady I-beam terminal cursor
	fmt.Print("\033[6 q")
	return nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.input.Cursor.SetMode(cursor.CursorStatic),
		m.spinner.Tick,
		setIBeamCursor,
		tea.Println(banner(m.eng.Agent.Conf.Name, m.eng.Agent.CurrentModel, m.sess.ID)),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			saveHistory(m.inputHist)
			return m, tea.Quit
		}
		if m.waiting {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyUp:
			if len(m.inputHist) > 0 {
				if m.histIdx == -1 {
					m.histBuf = m.input.Value()
					m.histIdx = len(m.inputHist) - 1
				} else if m.histIdx > 0 {
					m.histIdx--
				}
				m.input.SetValue(m.inputHist[m.histIdx])
				m.input.CursorEnd()
			}
			return m, nil
		case tea.KeyDown:
			if m.histIdx != -1 {
				if m.histIdx < len(m.inputHist)-1 {
					m.histIdx++
					m.input.SetValue(m.inputHist[m.histIdx])
				} else {
					m.histIdx = -1
					m.input.SetValue(m.histBuf)
				}
				m.input.CursorEnd()
			}
			return m, nil
		case tea.KeyTab:
			comps := m.completions()
			if len(comps) > 0 {
				// First tab: apply current (index 0)
				// Subsequent tabs: cycle through
				m.applyCompletion()
				m.compIdx = (m.compIdx + 1) % len(comps)
			}
			return m, nil
		case tea.KeyShiftTab:
			comps := m.completions()
			if len(comps) > 0 {
				m.compIdx = (m.compIdx - 1 + len(comps)) % len(comps)
				m.applyCompletion()
			}
			return m, nil
		case tea.KeyEnter:
			input := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			m.compIdx = 0
			m.histIdx = -1
			m.histBuf = ""
			if input == "" {
				return m, nil
			}
			m.inputHist = append(m.inputHist, input)
			if strings.HasPrefix(input, "/") {
				if input == "/quit" || input == "/exit" {
					saveHistory(m.inputHist)
					return m, tea.Quit
				}
				msg, quit := m.handleCommand(input)
				if quit {
					saveHistory(m.inputHist)
					return m, tea.Quit
				}
				// Return the message directly to Update
				return m.Update(msg)
			}
			// shell mode: execute command directly
			if m.shellMode {
				// Show command being executed
				return m, tea.Batch(
					printAbove(sTool.Render("$ ")+input),
					m.executeShellCmd(input),
				)
			}
			// chat mode: send to LLM
			m.waiting = true
			return m, tea.Batch(printAbove(sPrompt.Render("▶ ")+input), m.sendCmd(input))
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case streamChunkMsg:
		m.streaming += string(msg)
		return m, waitForStream(m.streamCh)

	case streamToolMsg:
		return m, tea.Batch(printAbove(sTool.Render("⚡ "+string(msg))), waitForStream(m.streamCh))

	case streamToolResultMsg:
		return m, tea.Batch(printAbove(sFaint.Render("  → "+string(msg))), waitForStream(m.streamCh))

	case streamDoneMsg:
		rendered := msg.content
		if m.renderer != nil {
			if out, err := m.renderer.Render(msg.content); err == nil {
				rendered = strings.TrimRight(out, "\n")
			}
		}
		m.streaming = ""
		m.waiting = false
		// trigger compression check
		if m.eng.NeedsCompression() {
			m.compressing = true
			return m, tea.Batch(printAbove(rendered), m.compressCmd())
		}
		return m, printAbove(rendered)

	case shellCwdMsg:
		m.shellCwd = string(msg)
		return m, printAbove(sFaint.Render(m.shellCwd))

	case compressDoneMsg:
		m.compressing = false
		return m, nil

	case compressErrMsg:
		m.compressing = false
		return m, printAbove(sErr.Render("⚠ compress: " + msg.err.Error()))

	case shellModeMsg:
		m.shellMode = msg.enable
		m.shellWithContext = msg.withContext
		if msg.enable {
			if msg.withContext {
				return m, printAbove(sOK.Render("✔ Entered shell mode with context (output will be added to conversation)"))
			}
			return m, printAbove(sOK.Render("✔ Entered shell mode (type '/chat' to return)"))
		}
		return m, printAbove(sOK.Render("✔ Returned to chat mode"))

	case shellOutputMsg:
		return m, printAbove(string(msg))

	case shellResultMsg:
		// Add to context if requested
		if msg.withContext {
			contextMsg := fmt.Sprintf("Shell command: %s\nOutput:\n%s", msg.command, msg.output)
			m.eng.Messages = append(m.eng.Messages, provider.Message{
				Role:    "user",
				Content: contextMsg,
			})
		}
		return m, printAbove(msg.output)

	case streamErrMsg:
		m.streaming = ""
		m.waiting = false
		return m, printAbove(sErr.Render("✘ " + msg.err.Error()))
	}

	prev := m.input.Value()
	if !m.waiting {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.input.Value() != prev {
		m.compIdx = 0
	}

	return m, tea.Batch(cmds...)
}

// wrapInput renders the textinput value with soft-wrap and a cursor.
func (m *model) wrapInput() string {
	prompt := sPrompt.Render("> ")
	promptW := 2 // "> " is 2 chars
	contentW := m.width - promptW
	if contentW < 1 {
		contentW = 1
	}

	val := m.input.Value()
	pos := m.input.Position()
	runes := []rune(val)

	// Insert a cursor marker
	const cur = "\x00"
	var buf strings.Builder
	for i, r := range runes {
		if i == pos {
			buf.WriteString(cur)
		}
		buf.WriteRune(r)
	}
	if pos >= len(runes) {
		buf.WriteString(cur)
	}
	text := buf.String()

	// Split into visual lines by display width
	textRunes := []rune(text)
	var lines []string
	for len(textRunes) > 0 {
		w := 0
		end := 0
		for end < len(textRunes) {
			r := textRunes[end]
			rw := 0
			if r != '\x00' {
				rw = runewidth.RuneWidth(r)
			}
			if w+rw > contentW && w > 0 {
				break
			}
			w += rw
			end++
		}
		if end == 0 {
			end = 1
		}
		lines = append(lines, string(textRunes[:end]))
		textRunes = textRunes[end:]
	}
	if len(lines) == 0 {
		lines = []string{cur}
	}

	// Render with cursor
	curStyle := lipgloss.NewStyle().Reverse(true)
	var out strings.Builder
	for i, line := range lines {
		pfx := "  "
		if i == 0 {
			pfx = prompt
		}
		// Replace cursor marker with styled cursor
		if strings.Contains(line, cur) {
			parts := strings.SplitN(line, cur, 2)
			ch := " "
			rest := parts[1]
			if len(rest) > 0 {
				r := []rune(rest)
				ch = string(r[0])
				rest = string(r[1:])
			}
			line = parts[0] + curStyle.Render(ch) + rest
		}
		out.WriteString(pfx + line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

func (m model) View() string {
	if m.waiting {
		if m.streaming != "" {
			return m.streaming + "\n" + m.spinner.View() + sFaint.Render(" streaming...")
		}
		return m.spinner.View() + sFaint.Render(" thinking...")
	}
	return m.wrapInput() + "\n" + m.statusBar()
}

// --- send to LLM ---

func waitForStream(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (m *model) sendCmd(input string) tea.Cmd {
	ch := make(chan tea.Msg, 64)
	m.streamCh = ch
	eng := m.eng

	go func() {
		var fullContent string
		err := eng.SendWithCallbacks(context.Background(), input,
			func(text string) {
				fullContent += text
				ch <- streamChunkMsg(text)
			},
			func(name string) {
				ch <- streamToolMsg(name)
			},
			func(preview string) {
				ch <- streamToolResultMsg(preview)
			},
		)
		if err != nil {
			ch <- streamErrMsg{err}
			return
		}
		if fullContent == "" {
			ch <- streamErrMsg{fmt.Errorf("empty response from model (no content received)")}
			return
		}
		ch <- streamDoneMsg{fullContent}
	}()

	return waitForStream(ch)
}

func (m *model) compressCmd() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		err := eng.Compress(context.Background(), nil)
		if err != nil {
			return compressErrMsg{err}
		}
		return compressDoneMsg{}
	}
}

// --- slash commands ---

func (m *model) handleCommand(input string) (tea.Msg, bool) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/shell":
		// Check for --context flag
		withContext := len(parts) > 1 && parts[1] == "--context"
		return shellModeMsg{enable: true, withContext: withContext}, false
	case "/chat":
		if m.shellMode {
			return shellModeMsg{enable: false, withContext: false}, false
		}
		return sErr.Render("Already in chat mode"), false
	case "/quit", "/exit":
		return "", true
	case "/clear":
		m.eng.Clear()
		return sOK.Render("✔ Conversation cleared"), false
	case "/skill":
		skills := m.eng.Agent.Conf.Skills
		if len(skills) == 0 {
			return sInfo.Render("No skills loaded"), false
		}
		var out []string
		for _, s := range skills {
			out = append(out, "  "+s)
		}
		return strings.Join(out, "\n"), false
	case "/mcp":
		mcps := m.eng.Agent.Conf.MCPs
		if len(mcps) == 0 {
			return sInfo.Render("No MCP servers configured"), false
		}
		var out []string
		for name, conf := range mcps {
			out = append(out, fmt.Sprintf("  %-15s %s", name, conf.URL))
		}
		return strings.Join(out, "\n"), false
	case "/help":
		var tools []string
		for _, t := range m.eng.Agent.ToolDefs {
			tools = append(tools, t.Name)
		}
		return sFaint.Render(fmt.Sprintf(`Session: %s
Tools:   %s

Commands:
  /agent list          List agents
  /agent <name>        Switch agent
  /model list          List models
  /model <name>        Switch model
  /skill               List loaded skills
  /mcp                 List MCP servers
  /shell               Enter shell mode (execute commands with tab completion)
  /shell --context     Enter shell mode and add output to conversation context
  /chat                Return to chat mode (from shell)
  /clear               Clear conversation
  /quit                Exit

Keys:
  ↑/↓                  Input history (on first/last line)
  Shift+Enter          New line
  Tab/Shift+Tab        Autocomplete
  Mouse wheel          Scroll screen

Shell Mode:
  - Tab completion for commands and paths (max 5 suggestions)
  - Use '/shell --context' to make LLM aware of command outputs
  - cd command changes directory
  - All bash features (pipes, redirects, etc.)
  - Type '/chat' to return to chat mode

Non-Interactive Mode Examples:
  gal-cli chat -m "your message"
  gal-cli chat -m @prompt.txt
  echo "test" | gal-cli chat -m -
  gal-cli chat --session abc -m "continue"
  gal-cli chat -a coder -m "write code" > output.txt`, m.sess.ID, strings.Join(tools, ", "))), false
	case "/agent":
		if len(parts) < 2 {
			return sInfo.Render("Agent: " + m.eng.Agent.Conf.Name), false
		}
		if parts[1] == "list" {
			names, err := config.ListAgents()
			if err != nil {
				return sErr.Render("✘ " + err.Error()), false
			}
			var out []string
			for _, n := range names {
				if n == m.eng.Agent.Conf.Name {
					out = append(out, sOK.Render("▶ ")+n)
				} else {
					out = append(out, "  "+n)
				}
			}
			return strings.Join(out, "\n"), false
		}
		newEng, err := buildEngine(m.cfg, parts[1], m.reg)
		if err != nil {
			return sErr.Render("✘ " + err.Error()), false
		}
		*m.eng = *newEng
		m.sess.Agent = m.eng.Agent.Conf.Name
		m.sess.Model = m.eng.Agent.CurrentModel
		return sOK.Render(fmt.Sprintf("✔ Agent: %s (model: %s)", m.eng.Agent.Conf.Name, m.eng.Agent.CurrentModel)), false
	case "/model":
		if len(parts) < 2 {
			return sInfo.Render("Model: " + m.eng.Agent.CurrentModel), false
		}
		if parts[1] == "list" {
			var out []string
			for _, mod := range m.eng.Agent.Conf.Models {
				if mod == m.eng.Agent.CurrentModel {
					out = append(out, sOK.Render("▶ ")+mod)
				} else {
					out = append(out, "  "+mod)
				}
			}
			return strings.Join(out, "\n"), false
		}
		newModel := parts[1]
		mp := strings.SplitN(newModel, "/", 2)
		if len(mp) != 2 {
			return sErr.Render("✘ invalid model format: " + newModel + " (expected provider/model)"), false
		}
		pConf, ok := m.cfg.Providers[mp[0]]
		if !ok {
			return sErr.Render("✘ unknown provider: " + mp[0]), false
		}
		var p provider.Provider
		switch pConf.Type {
		case "anthropic":
			p = &provider.Anthropic{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
		default:
			p = &provider.OpenAI{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
		}
		m.eng.Provider = p
		m.eng.SwitchModel(newModel)
		m.sess.Model = m.eng.Agent.CurrentModel
		return sOK.Render("✔ Model: " + m.eng.Agent.CurrentModel), false
	default:
		return sErr.Render("Unknown command: " + cmd + " (type /help)"), false
	}
}

// --- entry ---

func runChat(agentName, modelName, sessionID, message string, debug bool) error {
	session.Cleanup()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("run 'gal-cli init' first: %w", err)
	}
	if agentName == "" {
		agentName = cfg.DefaultAgent
	}
	reg := tool.NewRegistry()

	// load or create session
	var sess *session.Session
	var resumed bool
	if sessionID != "" {
		sess, err = session.Load(sessionID)
		if err == nil {
			resumed = true
			agentName = sess.Agent
		} else {
			sess = session.New(sessionID, agentName, "")
		}
	} else {
		sess = session.New(session.NewID(), agentName, "")
	}

	eng, err := buildEngine(cfg, agentName, reg)
	if err != nil {
		return err
	}

	// restore model from session if resuming
	if resumed && sess.Model != "" {
		// switch provider to match saved model
		mp := strings.SplitN(sess.Model, "/", 2)
		if len(mp) == 2 {
			if pConf, ok := cfg.Providers[mp[0]]; ok {
				var p provider.Provider
				switch pConf.Type {
				case "anthropic":
					p = &provider.Anthropic{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
				default:
					p = &provider.OpenAI{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
				}
				eng.Provider = p
				eng.SwitchModel(sess.Model)
			}
		}
		eng.Messages = sess.Messages
	}

	// override model if specified via flag
	if modelName != "" {
		mp := strings.SplitN(modelName, "/", 2)
		if len(mp) == 2 {
			if pConf, ok := cfg.Providers[mp[0]]; ok {
				var p provider.Provider
				switch pConf.Type {
				case "anthropic":
					p = &provider.Anthropic{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
				default:
					p = &provider.OpenAI{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
				}
				eng.Provider = p
				eng.SwitchModel(modelName)
			}
		}
	}

	sess.Model = eng.Agent.CurrentModel

	eng.ContextLimit = cfg.ContextLimit
	eng.Debug = debug
	if debug {
		eng.InitDebug()
	}
	defer eng.Close()

	// non-interactive mode
	if message != "" {
		return runOnce(eng, sess, message, debug)
	}

	// interactive mode
	m := initialModel(eng, cfg, reg, sess)
	p := tea.NewProgram(m)
	_, err = p.Run()
	fmt.Print("\033[0 q") // restore default cursor

	// save session on exit
	sess.Messages = eng.Messages
	sess.Agent = eng.Agent.Conf.Name
	sess.Model = eng.Agent.CurrentModel
	sess.Save()

	return err
}

func runOnce(eng *engine.Engine, sess *session.Session, message string, debug bool) error {
	// read message from various sources
	content, err := readMessage(message)
	if err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}

	// simple callbacks: stdout for LLM, stderr for tools
	onText := func(s string) {
		fmt.Print(s)
	}
	onToolCall := func(name string) {
		fmt.Fprintf(os.Stderr, "🔧 %s\n", name)
	}

	ctx := context.Background()
	err = eng.SendWithCallbacks(ctx, content, onText, onToolCall, nil)

	// save session
	sess.Messages = eng.Messages
	sess.Agent = eng.Agent.Conf.Name
	sess.Model = eng.Agent.CurrentModel
	sess.Save()

	if err == nil {
		fmt.Println() // trailing newline
		fmt.Fprintf(os.Stderr, "\n💾 Session: %s (resume with --session %s)\n", sess.ID, sess.ID)
	}
	return err
}

func readMessage(message string) (string, error) {
	// stdin
	if message == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// file
	if strings.HasPrefix(message, "@") {
		path := message[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// direct string
	return message, nil
}

// --- shell mode functions ---

func (m *model) shellCompletions() []string {
	val := m.input.Value()
	parts := strings.Fields(val)
	
	if len(parts) == 0 {
		return nil
	}
	
	// First word: complete command names
	if len(parts) == 1 && !strings.HasSuffix(val, " ") {
		return matchCommands(parts[0], 5)
	}
	
	// Other words: complete paths
	lastArg := parts[len(parts)-1]
	if strings.HasSuffix(val, " ") {
		lastArg = ""
	}
	return matchPaths(lastArg, 5)
}

func matchCommands(prefix string, limit int) []string {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return nil
	}
	
	seen := make(map[string]bool)
	var matches []string
	
	for _, dir := range strings.Split(pathEnv, ":") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, prefix) && !seen[name] {
				seen[name] = true
				matches = append(matches, name)
			}
		}
	}
	
	// Sort by relevance: shorter names (better match) first
	sort.Slice(matches, func(i, j int) bool {
		// Calculate match score: prefix_len / total_len
		scoreI := float64(len(prefix)) / float64(len(matches[i]))
		scoreJ := float64(len(prefix)) / float64(len(matches[j]))
		if scoreI != scoreJ {
			return scoreI > scoreJ // Higher score first
		}
		return matches[i] < matches[j] // Alphabetical as tiebreaker
	})
	
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func matchPaths(prefix string, limit int) []string {
	dir := "."
	base := prefix
	
	if strings.Contains(prefix, "/") {
		dir = filepath.Dir(prefix)
		base = filepath.Base(prefix)
	}
	
	// Expand ~ to home directory
	if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		dir = strings.Replace(dir, "~", home, 1)
	}
	
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base) {
			fullPath := filepath.Join(dir, name)
			if e.IsDir() {
				fullPath += "/"
			}
			// Make path relative if it was relative
			if !strings.HasPrefix(prefix, "/") && !strings.HasPrefix(prefix, "~") {
				fullPath = strings.TrimPrefix(fullPath, "./")
			}
			matches = append(matches, fullPath)
		}
	}
	
	// Sort by relevance: shorter names (better match) first
	sort.Slice(matches, func(i, j int) bool {
		baseI := filepath.Base(matches[i])
		baseJ := filepath.Base(matches[j])
		// Calculate match score
		scoreI := float64(len(base)) / float64(len(baseI))
		scoreJ := float64(len(base)) / float64(len(baseJ))
		if scoreI != scoreJ {
			return scoreI > scoreJ
		}
		// Directories first, then alphabetical
		isDirI := strings.HasSuffix(matches[i], "/")
		isDirJ := strings.HasSuffix(matches[j], "/")
		if isDirI != isDirJ {
			return isDirI
		}
		return matches[i] < matches[j]
	})
	
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func (m *model) executeShellCmd(input string) tea.Cmd {
	return func() tea.Msg {
		// Handle cd command specially
		if strings.HasPrefix(input, "cd ") || input == "cd" {
			path := strings.TrimSpace(strings.TrimPrefix(input, "cd"))
			if path == "" {
				home, _ := os.UserHomeDir()
				path = home
			}
			if strings.HasPrefix(path, "~") {
				home, _ := os.UserHomeDir()
				path = strings.Replace(path, "~", home, 1)
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(m.shellCwd, path)
			}
			if err := os.Chdir(path); err != nil {
				return shellOutputMsg(sErr.Render("✘ " + err.Error()))
			}
			// Update shellCwd
			newCwd, _ := os.Getwd()
			return shellCwdMsg(newCwd)
		}
		
		// Execute command with bash -i -c to load .bashrc and aliases
		// The -i flag makes it interactive, loading ~/.bashrc
		// Close stdin to prevent bash from waiting for input
		cmd := exec.Command("bash", "-i", "-c", input)
		cmd.Dir = m.shellCwd
		cmd.Stdin = nil // Don't connect stdin
		out, err := cmd.CombinedOutput()
		
		result := string(out)
		if err != nil && result == "" {
			result = err.Error()
		}
		
		if result == "" {
			result = sFaint.Render("(no output)")
		}
		
		return shellResultMsg{
			command:     input,
			output:      result,
			withContext: m.shellWithContext,
		}
	}
}

type shellCwdMsg string
type shellOutputMsg string
type shellResultMsg struct {
	command     string
	output      string
	withContext bool
}
type shellModeMsg struct {
	enable      bool
	withContext bool
}

func buildEngine(cfg *config.Config, agentName string, reg *tool.Registry) (*engine.Engine, error) {
	agentConf, err := config.LoadAgent(agentName)
	if err != nil {
		return nil, err
	}
	a, err := agent.Build(agentConf, reg)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(a.CurrentModel, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected provider/model)", a.CurrentModel)
	}
	pConf, ok := cfg.Providers[parts[0]]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", parts[0])
	}
	var p provider.Provider
	switch pConf.Type {
	case "anthropic":
		p = &provider.Anthropic{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
	default:
		p = &provider.OpenAI{APIKey: os.ExpandEnv(pConf.APIKey), BaseURL: pConf.BaseURL}
	}
	return engine.New(a, p), nil
}
