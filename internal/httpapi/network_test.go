package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/gpio"
	"github.com/rmto/raspberryrelay/internal/relay"
)

// fakeNetcfg is a NetworkConfigurator test double: no D-Bus, just enough
// behavior to exercise the confirm/rollback contract at the HTTP layer.
type fakeNetcfg struct {
	status    NetworkStatus
	applied   *config.NetworkIntent
	confirmed bool
	applyErr  error
}

func (f *fakeNetcfg) Get() (NetworkStatus, error) { return f.status, nil }
func (f *fakeNetcfg) Apply(intent config.NetworkIntent) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = &intent
	return nil
}
func (f *fakeNetcfg) Confirm() { f.confirmed = true }

func newTestServerWithNetcfg(t *testing.T, nc NetworkConfigurator) *Server {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(filepath.Join(dir, "config.json"))
	cfg := config.Default()
	if err := cfg.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := gpio.NewMock()
	controller, err := relay.New(m, cfg.Relays)
	if err != nil {
		t.Fatal(err)
	}
	return New(store, cfg, controller, nc, fstest.MapFS{}, "test")
}

func TestNetworkUnavailableWithoutConfigurator(t *testing.T) {
	s := newTestServerWithNetcfg(t, nil)
	auth := basicAuthHeader("admin", testPassword)
	w := doReq(t, s, "GET", "/api/network", "", auth)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no configurator, got %d", w.Code)
	}
}

func TestNetworkGetReflectsConfigurator(t *testing.T) {
	fake := &fakeNetcfg{status: NetworkStatus{Mode: "dhcp", Hostname: "relay-01"}}
	s := newTestServerWithNetcfg(t, fake)
	auth := basicAuthHeader("admin", testPassword)
	w := doReq(t, s, "GET", "/api/network", "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNetworkPutAppliesAndConfirm(t *testing.T) {
	fake := &fakeNetcfg{}
	s := newTestServerWithNetcfg(t, fake)
	auth := basicAuthHeader("admin", testPassword)

	body := `{"mode":"static","address":"192.168.1.50","netmask":"255.255.255.0","gateway":"192.168.1.1","dns":["1.1.1.1"],"hostname":"relay-01"}`
	w := doReq(t, s, "PUT", "/api/network", body, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.applied == nil || fake.applied.Address != "192.168.1.50" {
		t.Fatalf("expected Apply to be called with the new address, got %+v", fake.applied)
	}
	if fake.confirmed {
		t.Fatal("Confirm should not be called automatically by PUT /api/network")
	}

	w = doReq(t, s, "POST", "/api/network/confirm", "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !fake.confirmed {
		t.Fatal("expected Confirm to have been called")
	}
}

func TestNetworkPutRejectsInvalidStatic(t *testing.T) {
	fake := &fakeNetcfg{}
	s := newTestServerWithNetcfg(t, fake)
	auth := basicAuthHeader("admin", testPassword)

	body := `{"mode":"static","address":"not-an-ip","gateway":"192.168.1.1"}`
	w := doReq(t, s, "PUT", "/api/network", body, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid static address, got %d", w.Code)
	}
	if fake.applied != nil {
		t.Fatal("Apply must not be called when validation fails")
	}
}
