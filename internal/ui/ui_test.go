package ui

import (
	"fmt"
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
	status.Engine.Version = "1.2.3"
	status.Engine.Edition = "enterprise"
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
	for _, text := range []string{"GenesisDB", "orders", "RUNNING", "1.2.3", "42", "linux / arm64", "proxy running"} {
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
		m.status.License.Status = "You are using the free developer license."
		m.status.License.ValidUntil = "2099-12-31T23:59:59Z"
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
	m.createInputs[1].SetValue("secret")
	m.createFocus = 2
	_, cmd = m.handleCreateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal || !m.busy || cmd == nil {
		t.Fatalf("create wizard did not submit: mode=%v busy=%v cmd=%v", m.mode, m.busy, cmd)
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
