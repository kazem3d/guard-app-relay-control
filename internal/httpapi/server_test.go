package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/gpio"
	"github.com/rmto/raspberryrelay/internal/relay"
)

const testPassword = "correct-horse-battery"

func newTestServer(t *testing.T) (*Server, *config.Store, *gpio.Mock) {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(filepath.Join(dir, "config.json"))

	cfg := config.Default()
	cfg.Relays = []config.Relay{
		{ID: 1, Name: "Main Gate", GPIO: 17, DurationMS: 200, Enabled: true},
		{ID: 2, Name: "Parking Gate", GPIO: 27, DurationMS: 5000, Enabled: true},
	}
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

	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>index</html>")},
	}

	s := New(store, cfg, controller, nil, assets, "test")
	return s, store, m
}

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func doReq(t *testing.T, s *Server, method, path, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestUnauthenticatedRequestRejected(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doReq(t, s, "GET", "/api/relays", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") != "" {
		t.Error("must not send WWW-Authenticate (would trigger the native browser popup)")
	}
}

func TestWrongPasswordRejected(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doReq(t, s, "GET", "/api/relays", "", basicAuthHeader("admin", "wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestCorrectPasswordAccepted(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doReq(t, s, "GET", "/api/relays", "", basicAuthHeader("admin", testPassword))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMustChangeGateBlocksOtherRoutes(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(filepath.Join(dir, "config.json"))
	cfg, err := store.Load() // seeds admin/admin with MustChange=true
	if err != nil {
		t.Fatal(err)
	}
	m := gpio.NewMock()
	controller, err := relay.New(m, cfg.Relays)
	if err != nil {
		t.Fatal(err)
	}
	s := New(store, cfg, controller, nil, fstest.MapFS{}, "test")

	auth := basicAuthHeader("admin", config.DefaultPassword)

	w := doReq(t, s, "GET", "/api/relays", "", auth)
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 while must_change is set, got %d", w.Code)
	}

	// The password-change route itself must remain reachable.
	body := `{"current_password":"admin","new_password":"a-new-strong-password"}`
	w = doReq(t, s, "PUT", "/api/auth/password", body, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected password change to succeed, got %d: %s", w.Code, w.Body.String())
	}

	// After changing the password, the old credential must stop working
	// immediately (auth cache flush), and other routes must be reachable
	// with the new one.
	w = doReq(t, s, "GET", "/api/relays", "", auth)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected old credentials to be rejected after change, got %d", w.Code)
	}

	newAuth := basicAuthHeader("admin", "a-new-strong-password")
	w = doReq(t, s, "GET", "/api/relays", "", newAuth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with new credentials, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTriggerEndpointCoalesces(t *testing.T) {
	s, _, m := newTestServer(t)
	auth := basicAuthHeader("admin", testPassword)

	w1 := doReq(t, s, "POST", "/api/relay/1/trigger", "", auth)
	if w1.Code != http.StatusOK {
		t.Fatalf("first trigger: expected 200, got %d: %s", w1.Code, w1.Body.String())
	}
	var res1 relay.Result
	if err := json.Unmarshal(w1.Body.Bytes(), &res1); err != nil {
		t.Fatal(err)
	}
	if res1.Deduplicated {
		t.Error("first trigger should not be deduplicated")
	}
	if !m.State(17) {
		t.Fatal("relay should be active after trigger")
	}

	w2 := doReq(t, s, "POST", "/api/relay/1/trigger", "", auth)
	var res2 relay.Result
	if err := json.Unmarshal(w2.Body.Bytes(), &res2); err != nil {
		t.Fatal(err)
	}
	if !res2.Deduplicated {
		t.Error("immediate second trigger should be reported as deduplicated")
	}
}

func TestOffEndpointWorks(t *testing.T) {
	s, _, m := newTestServer(t)
	auth := basicAuthHeader("admin", testPassword)

	doReq(t, s, "POST", "/api/relay/1/trigger", "", auth)
	if !m.State(17) {
		t.Fatal("expected relay active")
	}
	w := doReq(t, s, "POST", "/api/relay/1/off", "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if m.State(17) {
		t.Error("relay should be inactive after /off")
	}
}

func TestUnknownRelayReturns404(t *testing.T) {
	s, _, _ := newTestServer(t)
	auth := basicAuthHeader("admin", testPassword)
	w := doReq(t, s, "POST", "/api/relay/999/trigger", "", auth)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeviceEndpoint(t *testing.T) {
	s, _, _ := newTestServer(t)
	auth := basicAuthHeader("admin", testPassword)
	w := doReq(t, s, "GET", "/api/device", "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var info map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["firmware"] != "test" {
		t.Errorf("expected firmware=test, got %v", info["firmware"])
	}
}

func TestLoginEndpoint(t *testing.T) {
	s, _, _ := newTestServer(t)

	w := doReq(t, s, "POST", "/api/login", `{"username":"admin","password":"`+testPassword+`"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = doReq(t, s, "POST", "/api/login", `{"username":"admin","password":"wrong"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStaticAssetServedUnauthenticated(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doReq(t, s, "GET", "/", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doReq(t, s, "GET", "/", "", "")
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("expected X-Frame-Options: DENY")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options: nosniff")
	}
}
