// Package relay implements the pulse state machine for a single relay
// output and the Controller that owns all configured relays.
//
// The defining behavior is how a repeated request is handled: a trigger
// that arrives while a pulse is already in flight is coalesced, not queued
// and not rejected — it reports the pulse already running and leaves the
// deadline untouched. A gate relay can never be held open longer than its
// configured duration by a double-click or a client retry. See Trigger.
package relay

import (
	"fmt"
	"sync"
	"time"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/gpio"
)

// State is a point-in-time snapshot of one relay, safe to serialise.
type State struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	GPIO        int    `json:"gpio"`
	Enabled     bool   `json:"enabled"`
	On          bool   `json:"on"`
	Latched     bool   `json:"latched"`      // true if held on by /on rather than mid-pulse
	RemainingMS int    `json:"remaining_ms"` // 0 if not pulsing
}

// Result is returned by Trigger/On/Off and doubles as part of the HTTP
// response body.
type Result struct {
	State        string `json:"state"` // "on" or "off"
	Deduplicated bool   `json:"deduplicated,omitempty"`
	RemainingMS  int    `json:"remaining_ms"`
}

// Event is published on the controller's event channel whenever a relay's
// on/off state changes, for the SSE stream.
type Event struct {
	RelayID int
	State   State
}

// relay is the live state for one configured relay. Each has its own mutex
// so a slow or stuck relay can never block operations on another.
type relay struct {
	mu   sync.Mutex
	cfg  config.Relay
	line gpio.Line

	on       bool
	latched  bool // held on by an explicit /on, not a timed pulse
	deadline time.Time
	timer    *time.Timer

	// gen is bumped on every state-changing operation and captured by each
	// pulse's expiry closure. A timer only acts if its gen still matches
	// the relay's current gen — this is what stops a timer from a
	// superseded pulse (e.g. one cancelled by /off or upgraded by /on)
	// from switching a line that a later command has already changed.
	gen uint64
}

// Controller owns every configured relay and fans out state-change events.
type Controller struct {
	backend gpio.Backend
	relays  map[int]*relay
	order   []int // preserves config order for listing

	events chan Event
}

// New opens a GPIO line for every enabled relay in relays and returns a
// ready Controller. Lines are requested with their inactive level as the
// initial output value (see gpio.Backend.RequestLine), so no relay is ever
// briefly energised during startup.
func New(backend gpio.Backend, relays []config.Relay) (*Controller, error) {
	c := &Controller{
		backend: backend,
		relays:  make(map[int]*relay, len(relays)),
		events:  make(chan Event, 64),
	}
	for _, rc := range relays {
		if !rc.Enabled {
			c.order = append(c.order, rc.ID)
			c.relays[rc.ID] = &relay{cfg: rc}
			continue
		}
		line, err := backend.RequestLine(rc.GPIO, rc.ActiveHigh)
		if err != nil {
			c.closeAll()
			return nil, fmt.Errorf("relay %d (gpio %d): %w", rc.ID, rc.GPIO, err)
		}
		c.order = append(c.order, rc.ID)
		c.relays[rc.ID] = &relay{cfg: rc, line: line}
	}
	return c, nil
}

// Events returns the channel of state-change notifications for the SSE
// handler to consume. It is never closed by the Controller.
func (c *Controller) Events() <-chan Event {
	return c.events
}

func (c *Controller) publish(id int, st State) {
	select {
	case c.events <- Event{RelayID: id, State: st}:
	default:
		// A slow/absent SSE consumer must never block a relay operation;
		// drop the event rather than backing up trigger latency.
	}
}

func (c *Controller) get(id int) (*relay, error) {
	r, ok := c.relays[id]
	if !ok {
		return nil, fmt.Errorf("relay/%d: %w", id, ErrNotFound)
	}
	return r, nil
}

// ErrNotFound is returned when a relay id isn't configured.
var ErrNotFound = fmt.Errorf("relay not found")

// ErrDisabled is returned when an operation targets a disabled relay.
var ErrDisabled = fmt.Errorf("relay disabled")

// Trigger pulses relay id for duration d (falling back to its configured
// default if d <= 0).
//
// If the relay is already energised by an in-flight pulse, the request is
// coalesced: it is not an error, the deadline is left untouched, and the
// response reports Deduplicated=true with the time remaining on the
// existing pulse. This is what makes a duplicate/retried request safe: two
// triggers 100ms apart on a 2s relay produce exactly one 2s pulse, not a
// 2.1s one.
//
// If the relay is currently latched on (via On), Trigger leaves it latched
// and reports the latched state without starting a timer — an explicit
// Off is required to close it, which is the more conservative choice when
// an operator has deliberately held a relay open.
func (c *Controller) Trigger(id int, d time.Duration) (Result, error) {
	r, err := c.get(id)
	if err != nil {
		return Result{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.line == nil {
		return Result{}, fmt.Errorf("relay/%d: %w", id, ErrDisabled)
	}

	if r.on && r.latched {
		return Result{State: "on"}, nil
	}
	if r.on {
		// Pulse already in flight: coalesce. The deadline is untouched.
		return Result{State: "on", Deduplicated: true, RemainingMS: msUntil(r.deadline)}, nil
	}

	if d <= 0 {
		d = time.Duration(r.cfg.DurationMS) * time.Millisecond
	}

	r.gen++
	gen := r.gen
	if err := r.line.SetActive(true); err != nil {
		return Result{}, fmt.Errorf("relay/%d: %w", id, err)
	}
	r.on = true
	r.latched = false
	r.deadline = time.Now().Add(d)
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(d, func() { c.expire(id, r, gen) })

	c.publish(id, snapshot(id, r))
	return Result{State: "on", RemainingMS: int(d / time.Millisecond)}, nil
}

// On latches relay id closed indefinitely, cancelling any in-flight pulse
// timer. Latching is a deliberate operator action and so overrides a timed
// pulse; only an explicit Off releases it.
func (c *Controller) On(id int) (Result, error) {
	r, err := c.get(id)
	if err != nil {
		return Result{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.line == nil {
		return Result{}, fmt.Errorf("relay/%d: %w", id, ErrDisabled)
	}

	r.gen++ // invalidate any pending pulse-expiry timer
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if !r.on {
		if err := r.line.SetActive(true); err != nil {
			return Result{}, fmt.Errorf("relay/%d: %w", id, err)
		}
	}
	r.on = true
	r.latched = true
	r.deadline = time.Time{}

	c.publish(id, snapshot(id, r))
	return Result{State: "on"}, nil
}

// Off de-energises relay id immediately, regardless of any in-flight pulse
// or latch. Off always wins: it is never coalesced, deferred, or rejected.
func (c *Controller) Off(id int) (Result, error) {
	r, err := c.get(id)
	if err != nil {
		return Result{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.line == nil {
		return Result{}, fmt.Errorf("relay/%d: %w", id, ErrDisabled)
	}

	r.gen++ // invalidate any pending pulse-expiry timer
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if err := r.line.SetActive(false); err != nil {
		return Result{}, fmt.Errorf("relay/%d: %w", id, err)
	}
	r.on = false
	r.latched = false
	r.deadline = time.Time{}

	c.publish(id, snapshot(id, r))
	return Result{State: "off"}, nil
}

// Get returns a snapshot of relay id's current state.
func (c *Controller) Get(id int) (State, error) {
	r, err := c.get(id)
	if err != nil {
		return State{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshot(id, r), nil
}

// List returns a snapshot of every configured relay, in config order.
func (c *Controller) List() []State {
	out := make([]State, 0, len(c.order))
	for _, id := range c.order {
		r := c.relays[id]
		r.mu.Lock()
		out = append(out, snapshot(id, r))
		r.mu.Unlock()
	}
	return out
}

// Shutdown cancels every timer and de-energises every relay, then releases
// the GPIO lines. Called on SIGTERM/SIGINT so no relay is left energised
// when the daemon exits cleanly. (A SIGKILL bypasses this — see the
// hardware note in the deployment docs about a pull resistor on the
// control side for that case.)
func (c *Controller) Shutdown() {
	for _, id := range c.order {
		r := c.relays[id]
		r.mu.Lock()
		r.gen++
		if r.timer != nil {
			r.timer.Stop()
			r.timer = nil
		}
		if r.line != nil {
			_ = r.line.SetActive(false)
		}
		r.on = false
		r.latched = false
		r.mu.Unlock()
	}
	c.closeAll()
}

func (c *Controller) closeAll() {
	for _, r := range c.relays {
		if r.line != nil {
			_ = r.line.Close()
			r.line = nil
		}
	}
}

// expire fires when a pulse timer elapses. It only acts if gen still
// matches the relay's current generation, so a timer belonging to a
// superseded pulse (cancelled by Off, or upgraded to a latch by On) can
// never switch a line state a later command already owns.
func (c *Controller) expire(id int, r *relay, gen uint64) {
	r.mu.Lock()
	if gen != r.gen {
		r.mu.Unlock()
		return
	}
	if r.line != nil {
		_ = r.line.SetActive(false)
	}
	r.on = false
	r.latched = false
	r.deadline = time.Time{}
	r.timer = nil
	st := snapshot(id, r)
	r.mu.Unlock()

	c.publish(id, st)
}

func snapshot(id int, r *relay) State {
	return State{
		ID:          id,
		Name:        r.cfg.Name,
		GPIO:        r.cfg.GPIO,
		Enabled:     r.line != nil,
		On:          r.on,
		Latched:     r.latched,
		RemainingMS: msUntil(r.deadline),
	}
}

func msUntil(deadline time.Time) int {
	if deadline.IsZero() {
		return 0
	}
	d := time.Until(deadline)
	if d < 0 {
		return 0
	}
	return int(d / time.Millisecond)
}
