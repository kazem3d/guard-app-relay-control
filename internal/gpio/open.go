package gpio

import (
	"fmt"
	"log"
	"os"
)

// Open selects a Backend. mode is one of "" (auto), "cdev", or "mock":
//
//   - "cdev" always uses the real character-device backend and fails if the
//     chip is unavailable.
//   - "mock" always uses the in-memory backend, regardless of hardware.
//   - "" (auto) tries the real chip and falls back to mock with a warning
//     if it can't be opened — this is what lets the daemon run unmodified
//     on a development machine with no GPIO hardware.
func Open(mode, chip string) (Backend, error) {
	switch mode {
	case "mock":
		return NewMock(), nil
	case "cdev":
		return OpenCDev(chip)
	case "", "auto":
		if _, err := os.Stat("/dev/" + chip); err != nil {
			log.Printf("gpio: %s not found, falling back to mock backend (%v)", chip, err)
			return NewMock(), nil
		}
		b, err := OpenCDev(chip)
		if err != nil {
			log.Printf("gpio: failed to open %s, falling back to mock backend: %v", chip, err)
			return NewMock(), nil
		}
		return b, nil
	default:
		return nil, fmt.Errorf("gpio: unknown backend mode %q", mode)
	}
}
