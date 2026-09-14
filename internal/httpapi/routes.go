package httpapi

import (
	"net/http"
)

func (s *Server) routes() {
	mux := http.NewServeMux()

	// Unauthenticated: the login endpoint itself, and static UI assets
	// (the UI can't fetch its own login page from behind auth).
	mux.HandleFunc("POST /api/login", s.securityHeaders(s.handleLogin))
	mux.Handle("/", s.securityHeaders(s.handleStatic))

	// Everything else requires Basic auth, then the must-change gate.
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return s.securityHeaders(s.requireAuth(s.requireNotMustChange(h)))
	}
	// The password-change endpoint must be reachable *during* a forced
	// must-change, so it only goes through auth, not the gate.
	mux.HandleFunc("PUT /api/auth/password", s.securityHeaders(s.requireAuth(s.handleChangePassword)))

	mux.HandleFunc("POST /api/relay/{id}/trigger", auth(s.handleTrigger))
	mux.HandleFunc("POST /api/relay/{id}/on", auth(s.handleOn))
	mux.HandleFunc("POST /api/relay/{id}/off", auth(s.handleOff))
	mux.HandleFunc("GET /api/relay/{id}", auth(s.handleGetRelay))
	mux.HandleFunc("GET /api/relays", auth(s.handleListRelays))
	mux.HandleFunc("PUT /api/relays", auth(s.handlePutRelays))

	mux.HandleFunc("GET /api/device", auth(s.handleDevice))

	mux.HandleFunc("GET /api/network", auth(s.handleGetNetwork))
	mux.HandleFunc("PUT /api/network", auth(s.handlePutNetwork))
	mux.HandleFunc("POST /api/network/confirm", auth(s.handleConfirmNetwork))

	mux.HandleFunc("GET /api/events", auth(s.handleEvents))

	s.mux = mux
}

// securityHeaders sets a small fixed set of defensive headers on every
// response. Cheap, and the UI is entirely self-contained so a strict CSP
// costs nothing here.
func (s *Server) securityHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'")
		h.Set("Referrer-Policy", "no-referrer")
		next(w, r)
	}
}

// requireNotMustChange blocks every route except password-change and the
// static UI while the account still holds its factory-default password. An
// appliance that ships with a known default password and lets that be
// skipped past is exactly how these boxes end up exposed on the open
// internet.
func (s *Server) requireNotMustChange(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Load().Auth.MustChange {
			writeError(w, http.StatusPreconditionRequired, "password change required", nil)
			return
		}
		next(w, r)
	}
}
