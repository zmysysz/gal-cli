package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	"github.com/gal-cli/gal-cli/internal/tool"
	"github.com/spf13/cobra"
)

func init() {
	var agentName string
	chatCmd := &cobra.Command{
		Use:   "chat",
		Short: "Start interactive chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(agentName)
		},
	}
	chatCmd.Flags().StringVarP(&agentName, "agent", "a", "", "Agent name (default: from config)")
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

func banner(agentName, modelName string) string {
	logo := sLogo.Render(`
   ██████╗  █████╗ ██╗      █████╗ ██╗  ██╗██╗   ██╗
  ██╔════╝ ██╔══██╗██║     ██╔══██╗╚██╗██╔╝╚██╗ ██╔╝
  ██║  ███╗███████║██║     ███████║ ╚███╔╝  ╚████╔╝
  ██║   ██║██╔══██║██║     ██╔══██║ ██╔██╗   ╚██╔╝
  ╚██████╔╝██║  ██║███████╗██║  ██║██╔╝ ██╗   ██║
   ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝`)

	info := sInfo.Render(fmt.Sprintf("  Agent: %s │ Model: %s", agentName, modelName))
	hints := sDim.Render("  /help commands │ /quit exit │ ↑↓ history │ Tab complete")

	return logo + "\n\n" + info + "\n" + hints
}

type streamChunkMsg string
type streamToolMsg string
type streamToolResultMsg string
type streamDoneMsg struct{ content string }
type streamErrMsg struct{ err error }

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

var slashCommands = []string{"/agent", "/model", "/clear", "/help", "/quit", "/exit"}

func (m *model) completions() []string {
	val := m.input.Value()
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
}

func initialModel(eng *engine.Engine, cfg *config.Config, reg *tool.Registry) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 0
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	ti.Cursor.TextStyle = lipgloss.NewStyle()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))

	m := model{
		eng: eng, cfg: cfg, reg: reg,
		input: ti, spinner: sp, renderer: r,
		histIdx: -1, inputHist: loadHistory(),
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
		tea.Println(banner(m.eng.Agent.Conf.Name, m.eng.Agent.CurrentModel)),
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
				m.compIdx = (m.compIdx + 1) % len(comps)
				m.applyCompletion()
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
				result, quit := m.handleCommand(input)
				if quit {
					saveHistory(m.inputHist)
					return m, tea.Quit
				}
				if result != "" {
					return m, printAbove(result)
				}
				return m, nil
			}
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
		return m, printAbove(rendered)

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
		ch <- streamDoneMsg{fullContent}
	}()

	return waitForStream(ch)
}

// --- slash commands ---

func (m *model) handleCommand(input string) (string, bool) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/quit", "/exit":
		return "", true
	case "/clear":
		m.eng.Clear()
		return sOK.Render("✔ Conversation cleared"), false
	case "/help":
		return sFaint.Render(`Commands:
  /agent list          List agents
  /agent <name>        Switch agent
  /model list          List models
  /model <name>        Switch model
  /clear               Clear conversation
  /quit                Exit

Keys:
  ↑/↓                  Input history (on first/last line)
  Shift+Enter          New line
  Tab/Shift+Tab        Autocomplete
  Mouse wheel          Scroll screen`), false
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
		m.eng.SwitchModel(parts[1])
		return sOK.Render("✔ Model: " + m.eng.Agent.CurrentModel), false
	default:
		return sErr.Render("Unknown command: " + cmd + " (type /help)"), false
	}
}

// --- entry ---

func runChat(agentName string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("run 'gal-cli init' first: %w", err)
	}
	if agentName == "" {
		agentName = cfg.DefaultAgent
	}
	reg := tool.NewRegistry()
	eng, err := buildEngine(cfg, agentName, reg)
	if err != nil {
		return err
	}
	m := initialModel(eng, cfg, reg)
	p := tea.NewProgram(m)
	_, err = p.Run()
	fmt.Print("\033[0 q") // restore default cursor
	return err
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
