package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/internal/worker"
)

func TestUIRearmsOnlyConsumedEventSource(t *testing.T) {
	ch := make(chan interface{}, 1)
	m := newModel(master.NewServer(), ch, "http://127.0.0.1:8080", 5, 1, worker.RequestOptions{})
	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: 80, Height: 24},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}},
	} {
		if _, cmd := m.Update(msg); cmd != nil {
			t.Fatalf("%T created another waiter or timer", msg)
		}
	}
	ch <- master.LogLine{Message: "next event"}
	_, cmd := m.Update(master.StatsUpdate{})
	if cmd == nil {
		t.Fatal("UI event did not rearm its waiter")
	}
	if _, ok := cmd().(master.LogLine); !ok {
		t.Fatal("UI event rearmed extra sources instead of one waiter")
	}
}
