// Package gpio abstracts the relay output lines behind a small interface so
// the rest of the daemon (and its tests) never depend on kernel gpiochip
// devices being present. On a Raspberry Pi, Backend is implemented by the
// character-device (cdev) backend in cdev.go. Anywhere else — this
// development machine included — Mock in mock.go stands in.
package gpio

// Line is one requested GPIO output line.
type Line interface {
	// SetActive drives the line to its logical "active" level, taking
	// ActiveHigh into account. active=true means "energise the relay".
	SetActive(active bool) error
	// Close releases the line. Implementations should leave the physical
	// pin in its inactive state before releasing it.
	Close() error
}

// Backend opens GPIO lines on one chip.
type Backend interface {
	// RequestLine reserves offset as an output, initialised to the inactive
	// level for activeHigh so there is no window where the pin glitches
	// active during startup.
	RequestLine(offset int, activeHigh bool) (Line, error)
	// Close releases the chip handle. Lines requested from it must be
	// closed first.
	Close() error
}
