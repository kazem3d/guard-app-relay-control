// Package httpapi implements the daemon's HTTP surface: the relay control
// endpoints from the brief, device/network info, authentication, and the
// SSE event stream the web UI uses for live state.
package httpapi

import (
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/relay"
)

// configHolder is a small atomic-swap wrapper around the current Config so
// handlers can read it without holding a lock across a request, and so a
// save from one handler is visible to the very next request from any other.
type configHolder struct {
	mu  sync.RWMutex
	cfg *config.Config
}

func newConfigHolder(cfg *config.Config) *configHolder {
	return &configHolder{cfg: cfg}
}

func (h *configHolder) Load() *config.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

func (h *configHolder) Store(cfg *config.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg = cfg
}

// NetworkConfigurator is the seam Server uses to read/apply network
// settings, implemented by internal/netcfg against NetworkManager. It is an
// interface here so the HTTP layer can be tested without D-Bus.
type NetworkConfigurator interface {
	Get() (NetworkStatus, error)
	Apply(intent config.NetworkIntent) error
	Confirm()
}

// NetworkStatus is the live network state as read from NetworkManager.
type NetworkStatus struct {
	Mode     string   `json:"mode"`
	Address  string   `json:"address"`
	Netmask  string   `json:"netmask"`
	Gateway  string   `json:"gateway"`
	DNS      []string `json:"dns"`
	Hostname string   `json:"hostname"`
}

// Server holds all dependencies for the HTTP API.
type Server struct {
	store      *config.Store
	cfg        *configHolder
	controller *relay.Controller
	netcfg     NetworkConfigurator // nil until Phase 5 wiring; handlers degrade gracefully
	version    string
	startTime  time.Time

	authCache *authCache
	throttle  *throttle

	assets fs.FS
	mux    *http.ServeMux
}

// New builds a Server. store and cfg must already reflect the currently
// loaded configuration; controller must be wired to the same relay set.
// assets is the web UI's static file tree (an embed.FS in production, a
// directory in development), rooted so that assets/index.html etc. resolve
// directly — see cmd/relayd for how it's constructed.
func New(store *config.Store, cfg *config.Config, controller *relay.Controller, netcfg NetworkConfigurator, assets fs.FS, version string) *Server {
	s := &Server{
		store:      store,
		cfg:        newConfigHolder(cfg),
		controller: controller,
		netcfg:     netcfg,
		assets:     assets,
		version:    version,
		startTime:  time.Now(),
		authCache:  newAuthCache(60 * time.Second),
		throttle:   newThrottle(),
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) logf(format string, args ...any) {
	log.Printf("httpapi: "+format, args...)
}
