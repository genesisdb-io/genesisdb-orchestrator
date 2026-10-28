package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/genesisdb-io/genesisdb-orchestrator/internal/orchestrator"
)

func TestFormatBytes(t *testing.T) {
	tests := map[uint64]string{
		0:           "0 B",
		1024:        "1.0 KiB",
		1024 * 1024: "1.0 MiB",
	}
	for value, want := range tests {
		if got := formatBytes(value); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", value, got, want)
		}
	}
}

func testStatus() orchestrator.Status {
	var status orchestrator.Status
	status.Engine.Version = "2.0.0"
	status.Engine.Channel = "stable"
	status.System.OS = "linux"
	status.System.Arch = "arm64"
	status.System.CPU.AvailableCores = 8
	status.System.CPU.UsedCores = 2
	status.System.Memory.Total = 1024 * 1024
	status.System.Memory.Used = 512 * 1024
	status.Events.Count = 42
	status.Events.Subjects = 7
	status.Events.Types = 3
	status.Storage.Format = "current"
	status.Storage.Ready = true
	return status
}

func TestDashboardRendersInstanceStatus(t *testing.T) {
	m := &model{
		instances: []orchestrator.Instance{{Name: "orders", Running: true, URL: "https://orders.genesisdb.local"}},
		status:    testStatus(),
		statusFor: "orders",
		proxy:     true,
		width:     130,
		height:    36,
	}
	view := ansi.Strip(m.View())
	if got := lipgloss.Width(view); got > m.width {
		t.Errorf("dashboard width = %d, terminal width = %d", got, m.width)
	}
	if got := lipgloss.Height(view); got > m.height {
		t.Errorf("dashboard height = %d, terminal height = %d", got, m.height)
	}
	for _, text := range []string{"GenesisDB", "orders", "RUNNING", "2.0.0", "42", "linux / arm64", "current", "proxy running"} {
		if !strings.Contains(view, text) {
			t.Errorf("dashboard does not contain %q", text)
		}
	}
}

func TestDashboardFitsResponsiveTerminals(t *testing.T) {
	for _, dimensions := range []struct{ width, height int }{{40, 14}, {50, 18}, {70, 24}, {80, 24}, {100, 28}, {100, 40}, {119, 36}, {130, 36}} {
		m := &model{
			instances: []orchestrator.Instance{{Name: "orders", Running: true, URL: "https://orders.genesisdb.local"}},
			status:    testStatus(),
			statusFor: "orders",
			proxy:     true,
			width:     dimensions.width,
			height:    dimensions.height,
		}
		view := m.View()
		if got := lipgloss.Width(view); got > dimensions.width {
			t.Errorf("%dx%d dashboard width = %d", dimensions.width, dimensions.height, got)
		}
		if got := lipgloss.Height(view); got > dimensions.height {
			t.Errorf("%dx%d dashboard height = %d", dimensions.width, dimensions.height, got)
		}
	}
}

func TestOverlaysFitTerminal(t *testing.T) {
	for _, overlay := range []mode{modeCreate, modeExport, modeImport, modeDelete} {
		m := &model{
			instances:    []orchestrator.Instance{{Name: "orders", Running: true, URL: "https://orders.genesisdb.local"}},
			input:        styledInput("path  ", "", 4096),
			createInputs: newCreateInputs(),
			mode:         overlay,
			width:        100,
			height:       30,
		}
		view := m.View()
		if got := lipgloss.Width(view); got > m.width {
			t.Errorf("overlay %v width = %d, terminal width = %d", overlay, got, m.width)
		}
		if got := lipgloss.Height(view); got > m.height {
			t.Errorf("overlay %v height = %d, terminal height = %d", overlay, got, m.height)
		}
	}
}

func TestHeaderDoesNotWrap(t *testing.T) {
	m := &model{proxy: false, width: 120}
	header := m.renderHeader()
	plain := ansi.Strip(header)
	if !strings.Contains(plain, "GenesisDB Orchestrator") {
		t.Fatalf("header does not use canonical product name: %q", plain)
	}
	if got := lipgloss.Width(header); got > 120 {
		t.Fatalf("header width = %d, want at most 120", got)
	}
	if got := lipgloss.Height(header); got != headerHeight {
		t.Fatalf("header height = %d, want %d", got, headerHeight)
	}
}

func TestCreateKeyOpensWizard(t *testing.T) {
	m := &model{createInputs: newCreateInputs()}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.mode != modeCreate || cmd == nil {
		t.Fatalf("create wizard did not open: mode=%v cmd=%v", m.mode, cmd)
	}
	m.createInputs[0].SetValue("orders")
	m.createInputs[1].SetValue("0123456789abcdef0123456789abcdef")
	m.createFocus = len(m.createInputs) - 1
	_, cmd = m.handleCreateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal || !m.busy || cmd == nil {
		t.Fatalf("create wizard did not submit: mode=%v busy=%v cmd=%v", m.mode, m.busy, cmd)
	}
}

// GenesisDB refuses tokens shorter than 32 bytes and the container would
// exit right after start, so the wizard rejects them before creating it.
func TestCreateWizardRejectsShortToken(t *testing.T) {
	m := &model{createInputs: newCreateInputs(), mode: modeCreate}
	m.createInputs[0].SetValue("orders")
	m.createInputs[1].SetValue("development-secret")
	m.createFocus = len(m.createInputs) - 1
	_, cmd := m.handleCreateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeCreate || m.busy || cmd != nil || m.err == nil {
		t.Fatalf("short token was accepted: mode=%v busy=%v cmd=%v err=%v", m.mode, m.busy, cmd, m.err)
	}
}

func TestBackgroundRefreshPausesDuringDialogs(t *testing.T) {
	m := &model{mode: modeCreate}
	_, cmd := m.Update(refreshMsg{})
	if m.loading {
		t.Fatal("background refresh started while a dialog was open")
	}
	if cmd == nil {
		t.Fatal("next background refresh was not scheduled")
	}
}

func TestDashboardAutomaticallyInitializesStoppedProxy(t *testing.T) {
	m := &model{}
	_, cmd := m.Update(instancesMsg{})
	if !m.autoInitAttempted || !m.busy || cmd == nil {
		t.Fatalf("automatic initialization did not start: attempted=%v busy=%v cmd=%v", m.autoInitAttempted, m.busy, cmd)
	}
}

func TestDashboardDoesNotRestartProxyAfterShutdown(t *testing.T) {
	m := &model{autoInitAttempted: true}
	_, _ = m.Update(instancesMsg{})
	if m.busy {
		t.Fatal("dashboard restarted proxy after the initial automatic initialization")
	}
}

func TestSuccessfulRefreshDoesNotClearActionError(t *testing.T) {
	original := fmt.Errorf("action failed")
	m := &model{err: original, autoInitAttempted: true}
	_, _ = m.Update(instancesMsg{})
	if m.err != original {
		t.Fatalf("refresh replaced action error with %v", m.err)
	}
}

func TestCompletePathsMatchesFilesAndDirectories(t *testing.T) {
	if home := completePaths("~"); len(home) != 1 || home[0] != "~"+string(os.PathSeparator) {
		t.Fatalf("home completion = %q", home)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backup.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	prefix := filepath.Join(dir, "ba")
	got := completePaths(prefix)
	want := []string{filepath.Join(dir, "backup.json"), filepath.Join(dir, "backups") + string(os.PathSeparator)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("completePaths(%q) = %q, want %q", prefix, got, want)
	}
	if hidden := completePaths(filepath.Join(dir, ".h")); len(hidden) != 1 || !strings.HasSuffix(hidden[0], ".hidden.json") {
		t.Fatalf("hidden completion = %q", hidden)
	}
}

func TestPaneScrollingIsIndependent(t *testing.T) {
	instances := make([]orchestrator.Instance, 12)
	for index := range instances {
		instances[index] = orchestrator.Instance{Name: fmt.Sprintf("database-%02d", index), Running: true}
	}
	m := &model{
		instances: instances,
		status:    testStatus(),
		statusFor: instances[0].Name,
		width:     100,
		height:    24,
	}

	m.scrollPane(leftPane, 3)
	if m.leftScroll == 0 || m.rightScroll != 0 {
		t.Fatalf("left scroll = %d, right scroll = %d", m.leftScroll, m.rightScroll)
	}
	leftScroll := m.leftScroll
	m.scrollPane(rightPane, 3)
	if m.rightScroll == 0 || m.leftScroll != leftScroll {
		t.Fatalf("left scroll = %d, right scroll = %d", m.leftScroll, m.rightScroll)
	}
}

func TestMouseWheelScrollsPaneUnderPointer(t *testing.T) {
	instances := make([]orchestrator.Instance, 12)
	for index := range instances {
		instances[index] = orchestrator.Instance{Name: fmt.Sprintf("database-%02d", index), Running: true}
	}
	m := &model{instances: instances, status: testStatus(), statusFor: instances[0].Name, width: 100, height: 24}
	leftWidth, _, _ := m.layout()

	_, _ = m.handleMouse(tea.MouseEvent{X: leftWidth + 2, Y: headerHeight + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.activePane != rightPane || m.rightScroll == 0 || m.leftScroll != 0 {
		t.Fatalf("right pane was not scrolled: active=%v left=%d right=%d", m.activePane, m.leftScroll, m.rightScroll)
	}
	_, _ = m.handleMouse(tea.MouseEvent{X: 3, Y: headerHeight + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.activePane != leftPane || m.leftScroll == 0 {
		t.Fatalf("left pane was not scrolled: active=%v left=%d", m.activePane, m.leftScroll)
	}
}

func TestClickSelectsDatabase(t *testing.T) {
	m := &model{
		instances: []orchestrator.Instance{{Name: "orders"}, {Name: "payments"}},
		width:     100,
		height:    24,
	}
	secondRowY := headerHeight + 2 + 3 + 3
	_, _ = m.handleMouse(tea.MouseEvent{X: 4, Y: secondRowY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
}

func TestFooterControlsAreClickable(t *testing.T) {
	m := &model{createInputs: newCreateInputs(), width: 100, height: 24}
	var x, y int
	found := false
	for row := 0; row < m.height && !found; row++ {
		for column := 0; column < m.width; column++ {
			if m.footerKeyAt(column, row) == "c" {
				x, y, found = column, row, true
				break
			}
		}
	}
	if !found {
		t.Fatal("create footer control has no clickable area")
	}
	_, _ = m.handleMouse(tea.MouseEvent{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.mode != modeCreate {
		t.Fatalf("mode = %v, want create", m.mode)
	}
}

func TestTabCyclesPathCompletions(t *testing.T) {
	m := &model{input: textInputForTest(), pathCompletions: []string{"first.json", "second.json"}, pathCompletion: -1}
	m.completePath(1)
	if m.input.Value() != "first.json" {
		t.Fatalf("first completion = %q", m.input.Value())
	}
	m.completePath(1)
	if m.input.Value() != "second.json" {
		t.Fatalf("second completion = %q", m.input.Value())
	}
	m.completePath(-1)
	if m.input.Value() != "first.json" {
		t.Fatalf("reverse completion = %q", m.input.Value())
	}
}

func TestExportKeyOpensPathDialog(t *testing.T) {
	m := &model{
		instances: []orchestrator.Instance{{Name: "orders", Running: true}},
		input:     textInputForTest(),
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if m.mode != modeExport {
		t.Fatalf("mode = %v, want export", m.mode)
	}
	if !strings.Contains(m.input.Value(), "orders-") {
		t.Fatalf("unexpected default backup path %q", m.input.Value())
	}
}

func textInputForTest() textinput.Model {
	input := textinput.New()
	input.CharLimit = 4096
	return input
}
