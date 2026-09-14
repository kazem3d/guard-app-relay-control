package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

// DefaultPassword is the factory password for the "admin" account. It is
// paired with Auth.MustChange = true so the appliance cannot be used until
// the operator picks a real password.
const DefaultPassword = "admin"

// Store loads and atomically persists a Config backed by a single JSON file.
type Store struct {
	path string
}

// NewStore returns a Store backed by path. It does not touch the filesystem.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads the config file, creating it with defaults (default account
// "admin"/"admin", MustChange: true) if it does not yet exist.
func (s *Store) Load() (*Config, error) {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		cfg := Default()
		if err := cfg.SetPassword(DefaultPassword); err != nil {
			return nil, fmt.Errorf("config: seeding default password: %w", err)
		}
		cfg.Auth.MustChange = true // SetPassword clears this; force it back on for the seeded default.
		if err := s.Save(cfg); err != nil {
			return nil, fmt.Errorf("config: writing default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", s.path, err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", s.path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %s is invalid: %w", s.path, err)
	}
	return cfg, nil
}

// Save validates cfg and writes it atomically: marshal -> write a temp file
// in the same directory -> fsync the temp file -> rename over the target ->
// fsync the parent directory. The same-directory temp file keeps the rename
// on one filesystem (so it's atomic), and the directory fsync is what makes
// the rename itself survive a power cut on the Pi's SD card, which is the
// normal way these appliances get turned off.
func (s *Store) Save(cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: refusing to save invalid config: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: creating %s: %w", dir, err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshaling: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Clean up the temp file on any early return; once the rename below
	// succeeds this is a no-op (the path no longer exists under this name).
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("config: writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: fsyncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("config: chmod temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("config: renaming into place: %w", err)
	}

	// fsync the directory entry so the rename itself is durable.
	df, err := os.Open(dir)
	if err != nil {
		// The rename already succeeded; a failure to open the directory for
		// fsync purposes is not worth failing the whole Save over.
		return nil
	}
	defer df.Close()
	_ = df.Sync()

	return nil
}

// SetPassword hashes and stores password, and clears MustChange.
func (c *Config) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c.Auth.PasswordHash = string(hash)
	c.Auth.MustChange = false
	return nil
}

// CheckPassword reports whether password matches the stored hash.
func (c *Config) CheckPassword(password string) bool {
	if c.Auth.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(c.Auth.PasswordHash), []byte(password)) == nil
}
