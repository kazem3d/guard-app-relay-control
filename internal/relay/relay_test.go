package relay

import (
	"testing"
	"time"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/gpio"
)

func newTestController(t *testing.T) (*Controller, *gpio.Mock) {
	t.Helper()
	m := gpio.NewMock()
	relays := []config.Relay{
		{ID: 1, Name: "Main Gate", GPIO: 17, DurationMS: 200, Enabled: true},
		{ID: 2, Name: "Parking Gate", GPIO: 27, DurationMS: 50, Enabled: true},
		{ID: 3, Name: "Disabled", GPIO: 22, DurationMS: 100, Enabled: false},
	}
	c, err := New(m, relays)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, m
}

// TestCoalesce is the central contract from the plan: a second trigger
// arriving 100ms into a 200ms pulse must not extend it. The relay must
// still open at the original 200ms deadline, and the duplicate call must
// report Deduplicated rather than erroring.
func TestCoalesceDoesNotExtendPulse(t *testing.T) {
	c, m := newTestController(t)

	start := time.Now()
	res1, err := c.Trigger(1, 0)
	if err != nil {
		t.Fatalf("first trigger: %v", err)
	}
	if res1.Deduplicated {
		t.Error("first trigger should not be marked deduplicated")
	}
	if !m.State(17) {
		t.Fatal("line should be active after first trigger")
	}

	time.Sleep(100 * time.Millisecond)

	res2, err := c.Trigger(1, 0)
	if err != nil {
		t.Fatalf("second trigger: %v", err)
	}
	if !res2.Deduplicated {
		t.Error("second (in-flight) trigger should be reported as deduplicated")
	}
	if res2.RemainingMS <= 0 || res2.RemainingMS > 110 {
		t.Errorf("expected ~100ms remaining, got %dms", res2.RemainingMS)
	}

	// Wait past the *original* deadline (200ms from start) plus margin.
	// If the second call had extended the timer, the line would still be
	// active at 220ms; it must not be.
	time.Sleep(200*time.Millisecond - time.Since(start) + 80*time.Millisecond)

	if m.State(17) {
		t.Error("relay should be off at/after the original deadline: coalescing must not extend the pulse")
	}

	// Exactly two transitions should have happened on this line: the
	// initial "on" from the first Trigger, and the expiry "off". The
	// second (deduplicated) Trigger call must not have produced a third
	// transition, and the off transition must land at ~200ms, not ~300ms
	// (which is what a re-armed/extended timer would produce).
	var offAt time.Time
	transitions := 0
	for _, tr := range m.Transitions {
		if tr.Offset != 17 {
			continue
		}
		transitions++
		if !tr.Active {
			offAt = tr.At
		}
	}
	if transitions != 2 {
		t.Fatalf("expected exactly 2 transitions on line 17 (on, off), got %d", transitions)
	}
	total := offAt.Sub(start)
	if total < 190*time.Millisecond || total > 260*time.Millisecond {
		t.Errorf("pulse closed at %v after start, expected ~200ms (coalescing must not extend it)", total)
	}
}

func TestOffAlwaysWinsDuringPulse(t *testing.T) {
	c, m := newTestController(t)

	if _, err := c.Trigger(1, 0); err != nil {
		t.Fatal(err)
	}
	if !m.State(17) {
		t.Fatal("expected line active after trigger")
	}

	res, err := c.Off(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "off" {
		t.Errorf("expected off, got %s", res.State)
	}
	if m.State(17) {
		t.Error("line should be inactive immediately after Off")
	}

	// Ensure the original pulse timer (now stale) doesn't do anything
	// surprising: re-trigger and confirm it starts a fresh pulse rather
	// than being blocked by a leftover timer.
	time.Sleep(250 * time.Millisecond) // past the original 200ms deadline
	res2, err := c.Trigger(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Deduplicated {
		t.Error("trigger after Off should start a fresh pulse, not be deduplicated")
	}
}

func TestOnLatchesAndCancelsPulseTimer(t *testing.T) {
	c, m := newTestController(t)

	if _, err := c.Trigger(2, 0); err != nil { // 50ms pulse
		t.Fatal(err)
	}
	res, err := c.On(2)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "on" {
		t.Errorf("expected on, got %s", res.State)
	}

	// Wait past the original pulse duration; the relay must still be on
	// because On() cancelled the pulse timer and latched it.
	time.Sleep(100 * time.Millisecond)
	if !m.State(27) {
		t.Error("latched relay should remain on past the original pulse duration")
	}

	st, err := c.Get(2)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Latched || !st.On {
		t.Errorf("expected latched on state, got %+v", st)
	}

	if _, err := c.Off(2); err != nil {
		t.Fatal(err)
	}
	if m.State(27) {
		t.Error("relay should be off after explicit Off")
	}
}

func TestTriggerOnDisabledRelayErrors(t *testing.T) {
	c, _ := newTestController(t)
	if _, err := c.Trigger(3, 0); err == nil {
		t.Error("expected error triggering a disabled relay")
	}
}

func TestTriggerUnknownRelayErrors(t *testing.T) {
	c, _ := newTestController(t)
	if _, err := c.Trigger(999, 0); err == nil {
		t.Error("expected error for unknown relay id")
	}
}

func TestShutdownDeenergisesAll(t *testing.T) {
	c, m := newTestController(t)

	if _, err := c.Trigger(1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.On(2); err != nil {
		t.Fatal(err)
	}
	c.Shutdown()

	if m.State(17) || m.State(27) {
		t.Error("all relays must be inactive after Shutdown")
	}
}

func TestEventsPublishedOnStateChange(t *testing.T) {
	c, _ := newTestController(t)
	events := c.Events()

	if _, err := c.Trigger(2, 0); err != nil { // 50ms pulse
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if ev.RelayID != 2 || !ev.State.On {
			t.Errorf("expected relay 2 on event, got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for trigger event")
	}

	select {
	case ev := <-events:
		if ev.RelayID != 2 || ev.State.On {
			t.Errorf("expected relay 2 off (expiry) event, got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expiry event")
	}
}
