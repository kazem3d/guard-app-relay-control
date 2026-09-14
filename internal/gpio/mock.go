package gpio

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Mock is a Backend that records line transitions in memory instead of
// touching real hardware. It is selected automatically when the configured
// gpiochip is absent, or explicitly via -gpio=mock / RELAYD_GPIO=mock. This
// is what makes the whole daemon (API, relay controller, web UI) runnable on
// a development machine with no GPIO hardware at all.
type Mock struct {
	mu          sync.Mutex
	lines       map[int]*mockLine
	Transitions []Transition // full history, for tests
}

// Transition records one SetActive call for inspection in tests.
type Transition struct {
	Offset int
	Active bool
	At     time.Time
}

// NewMock returns an empty Mock backend.
func NewMock() *Mock {
	return &Mock{lines: make(map[int]*mockLine)}
}

func (m *Mock) RequestLine(offset int, activeHigh bool) (Line, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.lines[offset]; exists {
		return nil, fmt.Errorf("gpio: mock offset %d already requested", offset)
	}
	l := &mockLine{backend: m, offset: offset, activeHigh: activeHigh}
	m.lines[offset] = l
	log.Printf("gpio(mock): line %d requested, initial state inactive", offset)
	return l, nil
}

func (m *Mock) Close() error { return nil }

// State reports whether offset is currently energised (active), for tests.
func (m *Mock) State(offset int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.lines[offset]
	if !ok {
		return false
	}
	return l.active
}

type mockLine struct {
	backend    *Mock
	offset     int
	activeHigh bool
	active     bool
}

func (l *mockLine) SetActive(active bool) error {
	l.backend.mu.Lock()
	defer l.backend.mu.Unlock()
	l.active = active
	l.backend.Transitions = append(l.backend.Transitions, Transition{Offset: l.offset, Active: active, At: time.Now()})
	level := "low"
	if active == l.activeHigh {
		level = "high"
	}
	log.Printf("gpio(mock): line %d -> %s (active=%v)", l.offset, level, active)
	return nil
}

func (l *mockLine) Close() error {
	err := l.SetActive(false)
	l.backend.mu.Lock()
	delete(l.backend.lines, l.offset)
	l.backend.mu.Unlock()
	return err
}
