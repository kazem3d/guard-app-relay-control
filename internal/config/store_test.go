package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := NewStore(path)

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Username != "admin" {
		t.Errorf("expected default username admin, got %q", cfg.Auth.Username)
	}
	if !cfg.Auth.MustChange {
		t.Errorf("expected MustChange true on seeded default")
	}
	if !cfg.CheckPassword(DefaultPassword) {
		t.Errorf("seeded default password does not verify")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected config file to be created: %v", err)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := NewStore(path)

	cfg := Default()
	if err := cfg.SetPassword("hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	cfg.Network.Hostname = "relay-test"
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Network.Hostname != "relay-test" {
		t.Errorf("hostname not persisted: got %q", loaded.Network.Hostname)
	}
	if !loaded.CheckPassword("hunter2hunter2") {
		t.Errorf("password not persisted correctly")
	}
	if loaded.Auth.MustChange {
		t.Errorf("MustChange should have been cleared by SetPassword")
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := NewStore(path)

	cfg := Default()
	cfg.Relays = append(cfg.Relays, dupRelay())
	if err := s.Save(cfg); err == nil {
		t.Fatal("expected Save to reject duplicate relay id")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("invalid config should not have been written")
	}
}

func dupRelay() Relay {
	return Relay{ID: 1, Name: "dup", GPIO: 99, DurationMS: 1000}
}

func TestSaveAtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := NewStore(path)

	cfg := Default()
	cfg.SetPassword("x")
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestValidateDurationBounds(t *testing.T) {
	cfg := Default()
	cfg.Relays = []Relay{{ID: 1, Name: "r", GPIO: 1, DurationMS: 10}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for too-short duration")
	}
	cfg.Relays[0].DurationMS = MaxDurationMS + 1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for too-long duration")
	}
}

func TestValidatePinCollision(t *testing.T) {
	cfg := Default()
	cfg.Relays = []Relay{
		{ID: 1, Name: "a", GPIO: 5, DurationMS: 1000},
		{ID: 2, Name: "b", GPIO: 5, DurationMS: 1000},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for duplicate gpio pin")
	}
}
