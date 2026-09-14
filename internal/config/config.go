// Package config loads and saves the daemon's single configuration document,
// /etc/relayd/config.json in production. There is no database: the file is
// rewritten atomically on every change and is the sole source of persisted
// state (NetworkManager itself is the source of truth for live network
// settings; see internal/netcfg).
package config

import (
	"encoding/json"
	"fmt"
	"net"
)

// Config is the top-level document persisted to config.json.
type Config struct {
	Version int           `json:"version"`
	Auth    Auth          `json:"auth"`
	HTTP    HTTP          `json:"http"`
	GPIO    GPIOConfig    `json:"gpio"`
	Relays  []Relay       `json:"relays"`
	Network NetworkIntent `json:"network"`
}

// Auth holds the single operator account. There is one account by design —
// this is an appliance with a physical audience, not a multi-tenant system.
type Auth struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"` // bcrypt
	MustChange   bool   `json:"must_change"`
}

// HTTP holds the listener configuration.
type HTTP struct {
	Listen string `json:"listen"`
}

// GPIOConfig selects the gpiochip device relays are opened on.
type GPIOConfig struct {
	Chip string `json:"chip"`
}

// Relay is one configured relay output.
type Relay struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	GPIO       int    `json:"gpio"`
	ActiveHigh bool   `json:"active_high"`
	DurationMS int    `json:"duration_ms"`
	DebounceMS int    `json:"debounce_ms"`
	Enabled    bool   `json:"enabled"`
}

// NetworkIntent records the last network configuration the operator asked
// for. NetworkManager is authoritative for what is actually applied; this
// block exists so a factory reset or first-boot script has a known address
// to fall back to.
type NetworkIntent struct {
	Mode     string   `json:"mode"` // "dhcp" or "static"
	Address  string   `json:"address"`
	Netmask  string   `json:"netmask"`
	Gateway  string   `json:"gateway"`
	DNS      []string `json:"dns"`
	Hostname string   `json:"hostname"`
}

const (
	// MinDurationMS / MaxDurationMS bound a relay's pulse duration.
	MinDurationMS = 50
	MaxDurationMS = 300_000 // 300s
)

// Default returns a fresh configuration with two example relays, matching
// the shape used throughout the README and the brief's own mockup. It is
// used to seed config.json on first boot.
func Default() *Config {
	return &Config{
		Version: 1,
		Auth: Auth{
			Username:   "admin",
			MustChange: true,
			// PasswordHash is filled in by the caller (Load, on first
			// creation) so the default-password hash lives in one place:
			// see SetPassword.
		},
		HTTP: HTTP{Listen: ":80"},
		GPIO: GPIOConfig{Chip: "gpiochip0"},
		Relays: []Relay{
			{ID: 1, Name: "Main Gate", GPIO: 17, ActiveHigh: false, DurationMS: 2000, Enabled: true},
			{ID: 2, Name: "Parking Gate", GPIO: 27, ActiveHigh: false, DurationMS: 5000, Enabled: true},
		},
		Network: NetworkIntent{Mode: "dhcp", Hostname: "relay-01"},
	}
}

// Validate checks internal consistency: unique relay IDs, unique GPIO pins,
// and duration bounds. It does not know which pins are actually wired or
// reserved on a given board — that is a runtime concern handled when GPIO
// lines are requested.
func (c *Config) Validate() error {
	if c.HTTP.Listen == "" {
		return fmt.Errorf("http.listen must not be empty")
	}
	if c.GPIO.Chip == "" {
		return fmt.Errorf("gpio.chip must not be empty")
	}
	ids := make(map[int]bool, len(c.Relays))
	pins := make(map[int]bool, len(c.Relays))
	for _, r := range c.Relays {
		if r.ID <= 0 {
			return fmt.Errorf("relay id must be positive, got %d", r.ID)
		}
		if ids[r.ID] {
			return fmt.Errorf("duplicate relay id %d", r.ID)
		}
		ids[r.ID] = true

		if r.GPIO < 0 {
			return fmt.Errorf("relay %d: gpio pin must not be negative", r.ID)
		}
		if pins[r.GPIO] {
			return fmt.Errorf("relay %d: gpio pin %d already used by another relay", r.ID, r.GPIO)
		}
		pins[r.GPIO] = true

		if r.DurationMS < MinDurationMS || r.DurationMS > MaxDurationMS {
			return fmt.Errorf("relay %d: duration_ms %d out of range [%d, %d]", r.ID, r.DurationMS, MinDurationMS, MaxDurationMS)
		}
		if r.DebounceMS < 0 {
			return fmt.Errorf("relay %d: debounce_ms must not be negative", r.ID)
		}
		if len(r.Name) > 64 {
			return fmt.Errorf("relay %d: name too long (max 64 chars)", r.ID)
		}
	}
	if c.Network.Mode == "static" {
		if net.ParseIP(c.Network.Address) == nil {
			return fmt.Errorf("network: invalid static address %q", c.Network.Address)
		}
		if net.ParseIP(c.Network.Gateway) == nil {
			return fmt.Errorf("network: invalid gateway %q", c.Network.Gateway)
		}
	}
	return nil
}

// Clone returns a deep copy, safe to mutate independently of c.
func (c *Config) Clone() *Config {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // Config is always marshalable; a failure here is a bug.
	}
	out := &Config{}
	if err := json.Unmarshal(b, out); err != nil {
		panic(err)
	}
	return out
}
