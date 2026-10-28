package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/genesisdb-io/genesisdb-orchestrator/internal/orchestrator"
)

func clickRendered(t *testing.T, m *model, label string) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if index := strings.Index(line, label); index >= 0 {
			_, _ = m.Update(tea.MouseMsg{X: lipgloss.Width(line[:index]), Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			return
		}
	}
	t.Fatalf("control %q not rendered", label)
}

func TestShutdownRestoresMouseAndQuitClick(t *testing.T) {
	m := &model{width: 100, height: 24, busy: true}
	_, cmd := m.Update(actionMsg{name: "orchestrator", action: "shut down", refresh: true})
	commands, ok := cmd().(tea.BatchMsg)
	if !ok || len(commands) == 0 || reflect.TypeOf(commands[0]()) != reflect.TypeOf(tea.EnableMouseCellMotion()) {
		t.Fatal("shutdown did not restore mouse reporting")
	}
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if index := strings.Index(line, "q  quit"); index >= 0 {
			_, quit := m.Update(tea.MouseMsg{X: lipgloss.Width(line[:index]), Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if quit == nil {
				t.Fatal("quit click ignored after shutdown")
			}
			if _, ok := quit().(tea.QuitMsg); !ok {
				t.Fatal("quit click did not quit")
			}
			return
		}
	}
	t.Fatal("quit control not rendered")
}

func TestRenderedClicks(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 18}, {100, 24}, {130, 36}, {50, 18}} {
		m := &model{width: size.w, height: size.h, createInputs: newCreateInputs()}
		clickRendered(t, m, "c  create")
		if m.mode != modeCreate {
			t.Fatal("click did not open create")
		}
		clickRendered(t, m, "esc  cancel")
		if m.mode != modeNormal {
			t.Fatal("click did not cancel create")
		}
	}
}

func TestShortPaneScrollPreservesSelection(t *testing.T) {
	m := &model{width: 100, height: 18, instances: []orchestrator.Instance{{Name: "orders", Running: true}}, statusFor: "orders", status: testStatus()}
	m.status.Storage.Format = "storage-at-bottom"
	if m.compact() {
		t.Fatal("short terminal hid panes")
	}
	before := m.View()
	for range 30 {
		_, _ = m.Update(tea.MouseMsg{X: 80, Y: 7, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	if m.selected != 0 || m.rightScroll == 0 {
		t.Fatal("wheel must scroll content without changing selection")
	}
	after := m.View()
	if before == after || !strings.Contains(ansi.Strip(after), "storage-at-bottom") {
		t.Fatal("bottom of details is unreachable")
	}
}

func TestRenderedDatabaseClickAfterScroll(t *testing.T) {
	m := &model{width: 100, height: 24, instances: []orchestrator.Instance{{Name: "orders"}, {Name: "payments"}, {Name: "inventory"}}, leftScroll: 3}
	clickRendered(t, m, "payments")
	if m.selected != 1 {
		t.Fatalf("selected %d instead of payments", m.selected)
	}
	selected := m.selected
	_, _ = m.Update(tea.MouseMsg{X: 8, Y: 7, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.selected != selected {
		t.Fatal("wheel changed selection")
	}
}
