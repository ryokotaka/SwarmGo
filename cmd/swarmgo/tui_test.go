package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/internal/worker"
	"github.com/ryokotaka/SwarmGo/proto"
)

func TestRenderLatencyDistinguishesPrecisionAndMissingSamples(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stats   workerStats
		running bool
		want    string
	}{
		{name: "sub-millisecond", stats: workerStats{success: 1, finished: true, latencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 900}}, want: "P50: 0.125 ms   P90: 0.750 ms   P99: 0.900 ms"},
		{name: "measured zero", stats: workerStats{success: 1, finished: true, latencyUS: &proto.LatencyMicros{}}, want: "P50: 0.000 ms   P90: 0.000 ms   P99: 0.000 ms"},
		{name: "progress only", stats: workerStats{success: 1}, running: true, want: "Latency: available when a worker finishes"},
		{name: "no successful samples", stats: workerStats{fail: 1, finished: true}, want: "Latency: no successful requests"},
		{name: "disconnected before final report", stats: workerStats{success: 1}, want: "Latency: no final report received"},
		{name: "legacy sub-millisecond", stats: workerStats{success: 1, finished: true}, want: "P50: <1 ms   P90: <1 ms   P99: <1 ms"},
		{name: "legacy milliseconds", stats: workerStats{success: 1, finished: true, latencyP90Ms: 1, latencyP99Ms: 2}, want: "P50: <1 ms   P90: 1 ms   P99: 2 ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model{expectedRequests: 1, running: tc.running, workerStats: map[string]workerStats{"worker": tc.stats}}
			if got := m.renderLatency(); !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderLatencySelectsHighestP99AndBreaksTiesByWorkerID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats map[string]workerStats
		want  string
	}{
		{
			name: "microsecond order survives identical legacy milliseconds",
			stats: map[string]workerStats{
				"a": {success: 1, finished: true, latencyP99Ms: 1, latencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 1750}},
				"z": {success: 1, finished: true, latencyP99Ms: 1, latencyUS: &proto.LatencyMicros{P50: 456, P90: 800, P99: 1800}},
			},
			want: "P50: 0.456 ms   P90: 0.800 ms   P99: 1.800 ms",
		},
		{
			name: "stable tie",
			stats: map[string]workerStats{
				"z": {success: 1, finished: true, latencyUS: &proto.LatencyMicros{P50: 456, P90: 800, P99: 900}},
				"a": {success: 1, finished: true, latencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 900}},
			},
			want: "P50: 0.125 ms   P90: 0.750 ms   P99: 0.900 ms",
		},
		{
			name: "mixed legacy and current workers",
			stats: map[string]workerStats{
				"a": {success: 1, finished: true, latencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 900}},
				"z": {success: 1, finished: true, latencyP50Ms: 1, latencyP90Ms: 2, latencyP99Ms: 3},
			},
			want: "P50: 1 ms   P90: 2 ms   P99: 3 ms",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model{expectedRequests: 2, workerStats: tc.stats}
			for i := 0; i < 30; i++ {
				if got := m.renderLatency(); !strings.Contains(got, tc.want) {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			}
		})
	}
}

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
