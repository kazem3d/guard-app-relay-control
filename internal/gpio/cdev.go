package gpio

import (
	"fmt"

	"github.com/warthog618/go-gpiocdev"
)

// CDev is a Backend implemented over the kernel's gpiochip character-device
// ABI via go-gpiocdev — the same ABI libgpiod wraps, but pure Go: no cgo, no
// libgpiod package needed on the Pi, and a single static cross-compiled
// binary.
type CDev struct {
	chip *gpiocdev.Chip
}

// OpenCDev opens the named chip (e.g. "gpiochip0").
func OpenCDev(name string) (*CDev, error) {
	chip, err := gpiocdev.NewChip(name, gpiocdev.WithConsumer("relayd"))
	if err != nil {
		return nil, fmt.Errorf("gpio: opening %s: %w", name, err)
	}
	return &CDev{chip: chip}, nil
}

func (c *CDev) RequestLine(offset int, activeHigh bool) (Line, error) {
	// The initial value is the *physical* line level. For an active-low
	// relay (activeHigh=false), "inactive" is physical high, so the
	// inactive initial value is 1; for active-high it's 0. Requesting the
	// line with this as part of gpiocdev.AsOutput sets the level
	// atomically at acquisition time — there is no window where the pin
	// glitches active during startup.
	initial := 0
	if !activeHigh {
		initial = 1
	}
	l, err := c.chip.RequestLine(offset, gpiocdev.AsOutput(initial))
	if err != nil {
		return nil, fmt.Errorf("gpio: requesting line %d: %w", offset, err)
	}
	return &cdevLine{line: l, activeHigh: activeHigh}, nil
}

func (c *CDev) Close() error {
	return c.chip.Close()
}

type cdevLine struct {
	line       *gpiocdev.Line
	activeHigh bool
}

func (l *cdevLine) SetActive(active bool) error {
	// Translate logical "active" (energise the relay) into the physical
	// level for this line's polarity.
	level := 0
	if active == l.activeHigh {
		level = 1
	}
	return l.line.SetValue(level)
}

func (l *cdevLine) Close() error {
	// Drive inactive before releasing so a released line never leaves the
	// relay energised.
	_ = l.SetActive(false)
	return l.line.Close()
}
