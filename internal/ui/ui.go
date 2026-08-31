// Package ui provides the interactive GenesisDB Orchestrator dashboard.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/genesisdb-io/genesisdb-orchestrator/internal/orchestrator"
)

const (
	refreshInterval = 5 * time.Second
	headerHeight    = 4
)

var (
	accent     = lipgloss.Color("#00B5D4")
	purple     = lipgloss.Color("#C6A7FF")
	green      = lipgloss.Color("#8BE9A8")
	yellow     = lipgloss.Color("#FFD37A")
	red        = lipgloss.Color("#FF7D90")
	text       = lipgloss.Color("#E7EDF6")
	muted      = lipgloss.Color("#7F8DA3")
	border     = lipgloss.Color("#334158")
	panelBg    = lipgloss.Color("#111824")
	selectedBg = lipgloss.Color("#23324A")
	chipBg     = lipgloss.Color("#202B3D")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle   = lipgloss.NewStyle().Foreground(muted)
	textStyle  = lipgloss.NewStyle().Foreground(text)
)

type mode int

const (
	modeNormal mode = iota
	modeImport
	modeExport
	modeDelete
	modeCreate
)

type model struct {
	app               *orchestrator.Orchestrator
	instances         []orchestrator.Instance
	selected          int
	proxy             bool
	status            orchestrator.Status
	statusFor         string
	statusErr         error
	loading           bool
	busy              bool
	autoInitAttempted bool
	mode              mode
	input             textinput.Model
	pathCompletions   []string
	pathCompletion    int
	createInputs      []textinput.Model
	createFocus       int
	spinner           spinner.Model
	message           string
	err               error
	width             int
	height            int
}

type instancesMsg struct {
	instances []orchestrator.Instance
	proxy     bool
	err       error
}

type statusMsg struct {
	name   string
	status orchestrator.Status
	err    error
}

type actionMsg struct {
	name    string
	action  string
	detail  string
	err     error
	refresh bool
}

type refreshMsg struct{}

// Run starts the full-screen dashboard.
func Run() error {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(accent)

	program := tea.NewProgram(&model{
		app:          orchestrator.New(),
		input:        styledInput("path  ", "", 4096),
		createInputs: newCreateInputs(),
		spinner:      spin,
		loading:      true,
	}, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.loadInstances(), m.spinner.Tick, scheduleRefresh())
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case refreshMsg:
		if m.busy || m.mode != modeNormal || m.loading {
			return m, scheduleRefresh()
		}
		m.loading = true
		return m, tea.Batch(m.loadInstances(), scheduleRefresh())
	case instancesMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.proxy = msg.proxy
		previous := m.selectedName()
		m.instances = msg.instances
		m.selected = indexByName(m.instances, previous)
		if m.selected >= len(m.instances) {
			m.selected = max(0, len(m.instances)-1)
		}
		if !m.autoInitAttempted {
			m.autoInitAttempted = true
			if !m.proxy {
				m.busy = true
				m.message, m.err = "", nil
				return m, proxyProcess(false)
			}
		}
		return m, m.loadSelectedStatus()
	case statusMsg:
		if msg.name != m.selectedName() {
			return m, nil
		}
		m.statusFor = msg.name
		m.status = msg.status
		m.statusErr = msg.err
		return m, nil
	case actionMsg:
		m.busy = false
		m.mode = modeNormal
		m.input.Blur()
		if msg.err != nil {
			m.err = msg.err
			m.message = ""
		} else {
			m.err = nil
			m.message = msg.action + " " + msg.name
			if msg.detail != "" {
				m.message += ": " + msg.detail
			}
		}
		if msg.refresh {
			return m, m.loadInstances()
		}
		return m, m.loadSelectedStatus()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeCreate {
		return m.handleCreateKey(key)
	}

	if m.mode == modeImport || m.mode == modeExport {
		switch key.String() {
		case "esc":
			m.mode = modeNormal
			m.err = nil
			m.clearPathCompletions()
			m.input.Blur()
			return m, nil
		case "tab":
			m.completePath(1)
			return m, nil
		case "shift+tab":
			m.completePath(-1)
			return m, nil
		case "enter":
			path := strings.TrimSpace(m.input.Value())
			if path == "" {
				m.err = fmt.Errorf("backup path cannot be empty")
				return m, nil
			}
			name := m.selectedName()
			selectedMode := m.mode
			m.mode = modeNormal
			m.err = nil
			m.clearPathCompletions()
			m.input.Blur()
			m.busy = true
			if selectedMode == modeImport {
				return m, m.importBackup(name, path)
			}
			return m, m.exportBackup(name, path)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		m.refreshPathCompletions()
		return m, cmd
	}

	if m.mode == modeDelete {
		switch key.String() {
		case "y", "Y":
			name := m.selectedName()
			m.mode = modeNormal
			m.busy = true
			return m, deleteProcess(name)
		case "n", "N", "esc":
			m.mode = modeNormal
			return m, nil
		}
		return m, nil
	}

	if m.busy {
		if key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
			m.clearStatus()
			return m, m.loadSelectedStatus()
		}
	case "down", "j":
		if m.selected+1 < len(m.instances) {
			m.selected++
			m.clearStatus()
			return m, m.loadSelectedStatus()
		}
	case "r":
		m.loading = true
		return m, m.loadInstances()
	case "s":
		instance, ok := m.selectedInstance()
		if !ok {
			return m, nil
		}
		m.busy = true
		m.message, m.err = "", nil
		if instance.Running {
			return m, m.stop(instance.Name)
		}
		return m, m.start(instance.Name)
	case "p":
		m.busy = true
		m.message, m.err = "", nil
		return m, proxyProcess(m.proxy)
	case "c":
		m.mode = modeCreate
		m.createFocus = 0
		m.err = nil
		for index := range m.createInputs {
			m.createInputs[index].SetValue("")
			m.createInputs[index].Blur()
		}
		return m, m.createInputs[0].Focus()
	case "d":
		if _, ok := m.selectedInstance(); ok {
			m.mode = modeDelete
		}
	case "e":
		instance, ok := m.selectedInstance()
		if ok && instance.Running {
			m.mode = modeExport
			m.input.SetValue(defaultBackupPath(instance.Name))
			m.input.CursorEnd()
			m.refreshPathCompletions()
			return m, m.input.Focus()
		}
	case "i":
		instance, ok := m.selectedInstance()
		if ok && instance.Running {
			m.mode = modeImport
			m.input.SetValue("")
			m.refreshPathCompletions()
			return m, m.input.Focus()
		}
	case "enter":
		return m, m.loadSelectedStatus()
	}
	return m, nil
}

func (m *model) handleCreateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.mode = modeNormal
		m.err = nil
		for index := range m.createInputs {
			m.createInputs[index].Blur()
		}
		return m, nil
	case "tab", "down":
		m.createInputs[m.createFocus].Blur()
		m.createFocus = (m.createFocus + 1) % len(m.createInputs)
		return m, m.createInputs[m.createFocus].Focus()
	case "shift+tab", "up":
		m.createInputs[m.createFocus].Blur()
		m.createFocus = (m.createFocus - 1 + len(m.createInputs)) % len(m.createInputs)
		return m, m.createInputs[m.createFocus].Focus()
	case "enter":
		if m.createFocus < len(m.createInputs)-1 {
			m.createInputs[m.createFocus].Blur()
			m.createFocus++
			return m, m.createInputs[m.createFocus].Focus()
		}
		name := strings.TrimSpace(m.createInputs[0].Value())
		token := strings.TrimSpace(m.createInputs[1].Value())
		license := m.createInputs[2].Value()
		if err := orchestrator.ValidateName(name); err != nil {
			m.err = err
			return m, nil
		}
		if token == "" {
			m.err = fmt.Errorf("auth token cannot be empty")
			return m, nil
		}
		m.err = nil
		m.mode = modeNormal
		m.busy = true
		for index := range m.createInputs {
			m.createInputs[index].Blur()
		}
		return m, createProcess(name, token, license)
	}
	var cmd tea.Cmd
	m.createInputs[m.createFocus], cmd = m.createInputs[m.createFocus].Update(key)
	return m, cmd
}

// View renders the dashboard.
func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading GenesisDB Orchestrator..."
	}
	if m.mode != modeNormal {
		return fit(m.renderOverlay(), m.width, m.height)
	}
	if m.width < 72 || m.height < 20 {
		return fit(m.renderCompact(), m.width, m.height)
	}

	leftWidth, rightWidth, bodyHeight := m.layout()
	panel := lipgloss.NewStyle().Height(max(1, bodyHeight-2)).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(border)
	contentHeight := max(1, bodyHeight-4)
	left := panel.Width(max(10, leftWidth-2)).Render(limitLines(m.renderDatabases(leftWidth-6, bodyHeight-4), contentHeight))
	right := panel.Width(max(10, rightWidth-2)).Render(limitLines(m.renderDetails(rightWidth-6), contentHeight))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return fit(lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter()), m.width, m.height)
}

func (m *model) layout() (leftWidth, rightWidth, bodyHeight int) {
	total := max(40, m.width)
	leftWidth = max(30, min(46, total*2/5))
	if leftWidth > total-40 {
		leftWidth = max(20, total/2)
	}
	rightWidth = total - leftWidth - 1
	bodyHeight = max(8, m.height-headerHeight-footerHeight(total))
	return leftWidth, rightWidth, bodyHeight
}

func (m *model) renderHeader() string {
	inner := max(10, m.width-4)
	left := titleStyle.Render("GenesisDB") + dimStyle.Render("  orchestrator")
	proxy := badge("proxy stopped", red)
	if m.proxy {
		proxy = badge("proxy running", green)
	}
	count := badge(fmt.Sprintf("%d databases", len(m.instances)), purple)
	right := proxy + " " + count
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > inner {
		right = proxy
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > inner {
		right = ""
	}
	gap := max(1, inner-lipgloss.Width(left)-lipgloss.Width(right))
	line := clamp(left+strings.Repeat(" ", gap)+right, inner)
	return lipgloss.NewStyle().Width(m.width).Padding(1, 2).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(border).Render(line)
}

func (m *model) renderDatabases(width, height int) string {
	width, height = max(10, width), max(3, height)
	header := panelTitle("databases", fmt.Sprintf("%d", len(m.instances)), width)
	if m.loading && len(m.instances) == 0 {
		return header + "\n\n" + m.spinner.View() + " loading containers"
	}
	if len(m.instances) == 0 {
		hints := []string{
			dimStyle.Render(clamp("No databases yet.", width)),
			"",
			clamp(shortcut("c", "create a database", accent), width),
			clamp(shortcut("p", "initialize the proxy", purple), width),
		}
		return header + "\n\n" + strings.Join(hints, "\n")
	}
	rows := max(1, (height-3)/3)
	start := 0
	if m.selected >= rows {
		start = m.selected - rows + 1
	}
	end := min(len(m.instances), start+rows)
	lines := []string{header, ""}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderDatabaseRow(i, width)...)
	}
	return strings.Join(lines, "\n")
}

func (m *model) renderDatabaseRow(index, width int) []string {
	instance := m.instances[index]
	marker := "  "
	titleRow := lipgloss.NewStyle().Foreground(text).Width(width)
	if index == m.selected {
		marker = lipgloss.NewStyle().Foreground(accent).Render("┃") + " "
		titleRow = titleRow.Bold(true).Foreground(accent).Background(selectedBg)
	}
	state, color := "STOPPED", muted
	if instance.Running {
		state, color = "RUNNING", green
	}
	host := strings.TrimPrefix(instance.URL, "https://")
	meta := "  " + lipgloss.NewStyle().Bold(true).Foreground(color).Render(state) + "  " + dimStyle.Render(host)
	return []string{
		titleRow.Render(clamp(marker+instance.Name, width)),
		clamp(meta, width),
		"",
	}
}

func (m *model) renderDetails(width int) string {
	width = max(10, width)
	instance, ok := m.selectedInstance()
	if !ok {
		welcome := []string{
			panelTitle("details", "none", width),
			"",
			clamp(lipgloss.NewStyle().Bold(true).Foreground(text).Render("welcome to ")+titleStyle.Render("GenesisDB"), width),
			dimStyle.Render(clamp("Run isolated databases behind one local HTTPS proxy.", width)),
			"",
			clamp(shortcut("c", "create a database", accent), width),
			clamp(shortcut("p", "initialize the proxy", purple), width),
		}
		return strings.Join(welcome, "\n")
	}

	stateBadge := badge("stopped", muted)
	if instance.Running {
		stateBadge = badge("running", green)
	}
	head := []string{
		panelTitle("details", instance.Name, width),
		clamp(lipgloss.NewStyle().Bold(true).Foreground(text).Render(instance.Name)+" "+stateBadge, width),
		dimStyle.Render(clamp(instance.URL, width)),
		"",
	}
	top := strings.Join(head, "\n")

	if !instance.Running {
		return top + "\n" + dimStyle.Render(clamp("This database is stopped.", width)) + "\n\n" + clamp(shortcut("s", "start this database", green), width)
	}
	if m.statusFor != instance.Name {
		return top + "\n" + m.spinner.View() + " loading /api/v1/status"
	}
	if m.statusErr != nil {
		return top + "\n" + badge("status unavailable", yellow) + "\n\n" + dimStyle.Render(clamp(firstLine(m.statusErr.Error()), width))
	}

	status := m.status
	engine := section("engine", width,
		entry("Version", empty(status.Engine.Version)),
		entry("Edition", empty(status.Engine.Edition)),
		entry("Channel", empty(status.Engine.Channel)),
	)
	events := section("event store", width,
		entry("Events", fmt.Sprintf("%d", status.Events.Count)),
		entry("Subjects", fmt.Sprintf("%d", status.Events.Subjects)),
		entry("Types", fmt.Sprintf("%d", status.Events.Types)),
		entry("Event data", formatBytes(uint64(status.Events.StorageSize))),
	)
	system := section("system", width,
		entry("Platform", status.System.OS+" / "+status.System.Arch),
		entry("CPU", fmt.Sprintf("%d of %d cores", status.System.CPU.UsedCores, status.System.CPU.AvailableCores)),
		entry("Memory", usageText(status.System.Memory)),
		entry("Storage", usageText(status.System.Storage)),
	)
	license := section("license", width,
		entry("Status", empty(status.License.Status)),
		entry("Valid until", formatValue(status.License.ValidUntil)),
	)

	if width >= 56 {
		columnWidth := (width - 2) / 2
		engine = section("engine", columnWidth,
			entry("Version", empty(status.Engine.Version)),
			entry("Edition", empty(status.Engine.Edition)),
			entry("Channel", empty(status.Engine.Channel)),
		)
		events = section("event store", columnWidth,
			entry("Events", fmt.Sprintf("%d", status.Events.Count)),
			entry("Subjects", fmt.Sprintf("%d", status.Events.Subjects)),
			entry("Types", fmt.Sprintf("%d", status.Events.Types)),
			entry("Event data", formatBytes(uint64(status.Events.StorageSize))),
		)
		system = section("system", columnWidth,
			entry("Platform", status.System.OS+" / "+status.System.Arch),
			entry("CPU", fmt.Sprintf("%d of %d cores", status.System.CPU.UsedCores, status.System.CPU.AvailableCores)),
			entry("Memory", usageText(status.System.Memory)),
			entry("Storage", usageText(status.System.Storage)),
		)
		license = section("license", columnWidth,
			entry("Status", empty(status.License.Status)),
			entry("Valid until", formatValue(status.License.ValidUntil)),
		)
		topRow := lipgloss.JoinHorizontal(lipgloss.Top, engine, "  ", events)
		bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, system, "  ", license)
		return top + "\n" + topRow + "\n\n" + bottomRow
	}
	return top + "\n" + engine + "\n\n" + events + "\n\n" + system + "\n\n" + license
}

func (m *model) renderFooter() string {
	inner := max(10, m.width-4)
	status := ""
	style := dimStyle
	switch {
	case m.busy:
		status = m.spinner.View() + " working"
	case m.err != nil:
		status = "error: " + firstLine(m.err.Error())
		style = lipgloss.NewStyle().Foreground(red)
	case m.message != "":
		status = m.message
		style = lipgloss.NewStyle().Foreground(green)
	default:
		running := 0
		for _, instance := range m.instances {
			if instance.Running {
				running++
			}
		}
		proxy := "proxy stopped"
		if m.proxy {
			proxy = "proxy running"
		}
		status = fmt.Sprintf("%d databases, %d running, %s", len(m.instances), running, proxy)
	}
	status = style.Render(clamp(status, inner))
	lines := append([]string{status, ""}, keyLines(inner)...)
	return lipgloss.NewStyle().Width(m.width).Padding(1, 2, 0, 2).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(border).Render(strings.Join(lines, "\n"))
}

func (m *model) renderCompact() string {
	width := max(20, m.width)
	inner := max(16, width-4)
	proxy := badge("proxy stopped", red)
	if m.proxy {
		proxy = badge("proxy running", green)
	}
	lines := []string{
		"",
		"  " + clamp(titleStyle.Render("GenesisDB")+dimStyle.Render("  orchestrator"), inner),
		"  " + clamp(proxy+" "+badge(fmt.Sprintf("%d databases", len(m.instances)), purple), inner),
		"",
	}
	instance, ok := m.selectedInstance()
	if !ok {
		lines = append(lines, "  "+dimStyle.Render(clamp("No databases yet.", inner)), "  "+clamp(shortcut("c", "create one", accent), inner))
	} else {
		state := badge("stopped", muted)
		if instance.Running {
			state = badge("running", green)
		}
		lines = append(lines,
			"  "+clamp(lipgloss.NewStyle().Bold(true).Foreground(accent).Render("┃ "+instance.Name)+" "+state, inner),
			"  "+dimStyle.Render(clamp(instance.URL, inner)),
		)
		if instance.Running && m.statusFor == instance.Name && m.statusErr == nil {
			lines = append(lines,
				"  "+pair(dimStyle.Render("Engine"), textStyle.Render(empty(m.status.Engine.Version)), inner),
				"  "+pair(dimStyle.Render("Events"), textStyle.Render(fmt.Sprintf("%d", m.status.Events.Count)), inner),
				"  "+pair(dimStyle.Render("Memory"), textStyle.Render(usageText(m.status.System.Memory)), inner),
			)
		}
	}
	switch {
	case m.err != nil:
		lines = append(lines, "", "  "+lipgloss.NewStyle().Foreground(red).Render(clamp("error: "+firstLine(m.err.Error()), inner)))
	case m.message != "":
		lines = append(lines, "", "  "+lipgloss.NewStyle().Foreground(green).Render(clamp(m.message, inner)))
	}
	lines = append(lines, "")
	for _, line := range keyLines(inner) {
		lines = append(lines, "  "+line)
	}
	return strings.Join(lines, "\n")
}

func (m *model) renderOverlay() string {
	width := max(1, min(66, m.width-4))
	inner := max(1, width-4)
	borderColor := purple
	var lines []string
	switch m.mode {
	case modeCreate:
		lines = []string{
			panelTitle("create database", "wizard", inner),
			dimStyle.Render(clamp("The database starts immediately after creation.", inner)),
			"",
			inputView(m.createInputs[0], inner),
			inputView(m.createInputs[1], inner),
			inputView(m.createInputs[2], inner),
			"",
			shortcut("tab", "switch", purple) + "  " + shortcut("enter", "create", green) + "  " + shortcut("esc", "cancel", muted),
		}
	case modeExport:
		lines = []string{
			panelTitle("export backup", m.selectedName(), inner),
			dimStyle.Render(clamp("The backup is written as a JSON file.", inner)),
			"",
			inputView(m.input, inner),
		}
		lines = append(lines, m.renderPathCompletions(inner)...)
		lines = append(lines, "", shortcut("tab", "complete", purple)+"  "+shortcut("enter", "export", green)+"  "+shortcut("esc", "cancel", muted))
	case modeImport:
		lines = []string{
			panelTitle("import backup", m.selectedName(), inner),
			dimStyle.Render(clamp("Restore only works into an empty event store.", inner)),
			"",
			inputView(m.input, inner),
		}
		lines = append(lines, m.renderPathCompletions(inner)...)
		lines = append(lines, "", shortcut("tab", "complete", purple)+"  "+shortcut("enter", "import", green)+"  "+shortcut("esc", "cancel", muted))
	case modeDelete:
		borderColor = red
		lines = []string{
			panelTitle("delete database", m.selectedName(), inner),
			dimStyle.Render(clamp("This permanently removes the container and", inner)),
			dimStyle.Render(clamp("its data volume.", inner)),
			"",
			shortcut("y", "delete now", red) + "  " + shortcut("n", "cancel", muted),
		}
	}
	if m.err != nil && (m.mode == modeCreate || m.mode == modeExport || m.mode == modeImport) {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(red).Render(clamp(firstLine(m.err.Error()), inner)))
	}
	box := lipgloss.NewStyle().Width(width).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Background(panelBg).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceChars(" "))
}

func (m *model) loadInstances() tea.Cmd {
	return func() tea.Msg {
		instances, err := m.app.Instances()
		if err != nil {
			return instancesMsg{err: err}
		}
		proxy, err := m.app.ProxyRunning()
		return instancesMsg{instances: instances, proxy: proxy, err: err}
	}
}

func (m *model) loadSelectedStatus() tea.Cmd {
	instance, ok := m.selectedInstance()
	if !ok || !instance.Running {
		return nil
	}
	name := instance.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, err := m.app.Status(ctx, name)
		return statusMsg{name: name, status: status, err: err}
	}
}

func (m *model) start(name string) tea.Cmd {
	return func() tea.Msg {
		err := m.app.Start(name)
		return actionMsg{name: name, action: "started", err: err, refresh: true}
	}
}

func (m *model) stop(name string) tea.Cmd {
	return func() tea.Msg {
		err := m.app.StopInstance(name)
		return actionMsg{name: name, action: "stopped", err: err, refresh: true}
	}
}

func (m *model) exportBackup(name, path string) tea.Cmd {
	return func() tea.Msg {
		absolute, err := filepath.Abs(expandHome(path))
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			err = m.app.ExportBackup(ctx, name, absolute)
		}
		return actionMsg{name: name, action: "exported", detail: absolute, err: err}
	}
}

func (m *model) importBackup(name, path string) tea.Cmd {
	return func() tea.Msg {
		absolute, err := filepath.Abs(expandHome(path))
		if err == nil {
			err = m.app.ImportBackup(context.Background(), name, absolute)
		}
		return actionMsg{name: name, action: "imported", detail: absolute, err: err}
	}
}

func proxyProcess(running bool) tea.Cmd {
	executable, err := os.Executable()
	action := "initialized"
	commandName := "init"
	if running {
		action = "shut down"
		commandName = "shutdown"
	}
	if err != nil {
		return func() tea.Msg { return actionMsg{name: "orchestrator", action: action, err: err, refresh: true} }
	}
	command := exec.Command(executable, commandName)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return actionMsg{name: "orchestrator", action: action, err: err, refresh: true}
	})
}

func createProcess(name, token, license string) tea.Cmd {
	executable, err := os.Executable()
	if err != nil {
		return func() tea.Msg { return actionMsg{name: name, action: "created", err: err, refresh: true} }
	}
	command := exec.Command(executable, "create", name, "--auth-token", token, "--license-key", license)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return actionMsg{name: name, action: "created", err: err, refresh: true}
	})
}

func deleteProcess(name string) tea.Cmd {
	executable, err := os.Executable()
	if err != nil {
		return func() tea.Msg { return actionMsg{name: name, action: "deleted", err: err, refresh: true} }
	}
	command := exec.Command(executable, "delete", name)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return actionMsg{name: name, action: "deleted", err: err, refresh: true}
	})
}

func scheduleRefresh() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return refreshMsg{} })
}

func (m *model) selectedInstance() (orchestrator.Instance, bool) {
	if m.selected < 0 || m.selected >= len(m.instances) {
		return orchestrator.Instance{}, false
	}
	return m.instances[m.selected], true
}

func (m *model) selectedName() string {
	instance, ok := m.selectedInstance()
	if !ok {
		return ""
	}
	return instance.Name
}

func (m *model) clearStatus() {
	m.statusFor = ""
	m.statusErr = nil
}

func indexByName(instances []orchestrator.Instance, name string) int {
	for index, instance := range instances {
		if instance.Name == name {
			return index
		}
	}
	return 0
}

func newCreateInputs() []textinput.Model {
	name := styledInput("name     ", "orders", 63)
	token := styledInput("token    ", "development-secret", 512)
	license := styledInput("license  ", "press enter for the free license", 4096)
	license.EchoMode = textinput.EchoPassword
	license.EchoCharacter = '*'
	return []textinput.Model{name, token, license}
}

func styledInput(prompt, placeholder string, limit int) textinput.Model {
	input := textinput.New()
	input.Prompt = prompt
	input.Placeholder = placeholder
	input.CharLimit = limit
	input.Width = 46
	input.PromptStyle = lipgloss.NewStyle().Foreground(muted)
	input.TextStyle = textStyle
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(border)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(accent)
	return input
}

func inputView(input textinput.Model, width int) string {
	input.Width = max(8, width-lipgloss.Width(input.Prompt)-2)
	return clamp(input.View(), width)
}

func (m *model) refreshPathCompletions() {
	m.pathCompletions = completePaths(m.input.Value())
	m.pathCompletion = -1
}

func (m *model) clearPathCompletions() {
	m.pathCompletions = nil
	m.pathCompletion = -1
}

func (m *model) completePath(direction int) {
	if len(m.pathCompletions) == 0 {
		m.refreshPathCompletions()
	}
	if len(m.pathCompletions) == 0 {
		return
	}
	if m.pathCompletion < 0 {
		if direction < 0 {
			m.pathCompletion = len(m.pathCompletions) - 1
		} else {
			m.pathCompletion = 0
		}
	} else {
		m.pathCompletion = (m.pathCompletion + direction + len(m.pathCompletions)) % len(m.pathCompletions)
	}
	m.input.SetValue(m.pathCompletions[m.pathCompletion])
	m.input.CursorEnd()
}

func (m *model) renderPathCompletions(width int) []string {
	if len(m.pathCompletions) == 0 {
		return nil
	}
	start := 0
	if m.pathCompletion >= 4 {
		start = m.pathCompletion - 3
	}
	end := min(len(m.pathCompletions), start+4)
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		prefix := "  "
		style := dimStyle
		if index == m.pathCompletion {
			prefix = "› "
			style = lipgloss.NewStyle().Foreground(accent)
		}
		lines = append(lines, style.Render(clamp(prefix+m.pathCompletions[index], width)))
	}
	return lines
}

func completePaths(value string) []string {
	if value == "~" {
		return []string{"~" + string(os.PathSeparator)}
	}
	separatorAt := strings.LastIndexAny(value, `/\\`)
	typedDir, prefix := "", value
	if separatorAt >= 0 {
		typedDir = value[:separatorAt+1]
		prefix = value[separatorAt+1:]
	}
	resolvedDir := "."
	if typedDir != "" {
		resolvedDir = expandHome(typedDir)
	}
	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		return nil
	}
	separator := string(os.PathSeparator)
	if strings.HasSuffix(typedDir, `/`) {
		separator = "/"
	} else if strings.HasSuffix(typedDir, `\\`) {
		separator = `\\`
	}
	matches := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		candidate := typedDir + name
		if entry.IsDir() {
			candidate += separator
		}
		matches = append(matches, candidate)
	}
	return matches
}

func panelTitle(title, tag string, width int) string {
	head := titleStyle.Render(title)
	label := badge(tag, purple)
	if tag == "" || lipgloss.Width(head)+lipgloss.Width(label)+1 > width {
		label = ""
	}
	gap := max(1, width-lipgloss.Width(head)-lipgloss.Width(label))
	row := clamp(head+strings.Repeat(" ", gap)+label, width)
	divider := lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("─", max(1, width)))
	return row + "\n" + divider
}

func keyLines(width int) []string {
	items := []string{
		shortcut("↑↓", "select", accent),
		shortcut("c", "create", accent),
		shortcut("s", "start/stop", green),
		shortcut("p", "init/shutdown", purple),
		shortcut("e", "export", yellow),
		shortcut("i", "import", yellow),
		shortcut("d", "delete", red),
		shortcut("r", "refresh", accent),
		shortcut("q", "quit", muted),
	}
	var lines []string
	current, used := "", 0
	for _, item := range items {
		w := lipgloss.Width(item)
		if current != "" && used+2+w > width {
			lines = append(lines, current)
			current, used = item, w
			continue
		}
		if current == "" {
			current, used = item, w
			continue
		}
		current += "  " + item
		used += 2 + w
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func footerHeight(width int) int {
	return 4 + len(keyLines(max(10, width-4)))
}

type detailEntry struct {
	label string
	value string
}

func entry(label, value string) detailEntry {
	return detailEntry{label: label, value: value}
}

func section(title string, width int, rows ...detailEntry) string {
	lines := []string{panelTitle(title, "", width)}
	for _, row := range rows {
		lines = append(lines, pair(dimStyle.Render(row.label), textStyle.Render(row.value), width))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func pair(left, right string, width int) string {
	left = clamp(left, width)
	if right == "" {
		return left
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > width {
		return clamp(left+" "+right, width)
	}
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

func badge(label string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Bold(true).Foreground(color).Background(chipBg).Padding(0, 1).Render(label)
}

func shortcut(key, label string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Bold(true).Foreground(color).Background(chipBg).Padding(0, 1).Render(key) + dimStyle.Render(" "+label)
}

func clamp(value string, width int) string {
	return lipgloss.NewStyle().MaxWidth(max(1, width)).Render(value)
}

func fit(view string, width, height int) string {
	width, height = max(1, width), max(1, height)
	lines := strings.Split(view, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]
	for index, line := range lines {
		line = clamp(line, width)
		lines[index] = line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
	}
	return strings.Join(lines, "\n")
}

func limitLines(content string, limit int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= limit {
		return content
	}
	return strings.Join(lines[:max(1, limit)], "\n")
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func defaultBackupPath(name string) string {
	return filepath.Join("backups", fmt.Sprintf("%s-%s.json", name, time.Now().Format("20060102-150405")))
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func usageText(usage orchestrator.Usage) string {
	total := usage.Total
	if total == 0 {
		total = usage.Max
	}
	return formatBytes(usage.Used) + " / " + formatBytes(total)
}

func formatBytes(value uint64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	for _, unit := range units {
		size /= 1024
		if size < 1024 {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
	}
	return fmt.Sprintf("%.1f PiB", size/1024)
}

func formatValue(value interface{}) string {
	if value == nil || value == false || value == "" {
		return "not available"
	}
	return fmt.Sprint(value)
}

func empty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not available"
	}
	return value
}
