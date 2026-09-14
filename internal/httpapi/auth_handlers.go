package httpapi

import (
	"net/http"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	MustChange bool `json:"must_change"`
}

// handleLogin exists for the login page to give the operator a real "wrong
// password" message before it starts sending Basic auth on every request;
// it performs the same check requireAuth does, but does not itself create a
// session — the browser-side login page is responsible for storing the
// Authorization header value it can now construct and attaching it to
// subsequent calls.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	ip := clientIP(r)
	if d := s.throttle.delay(ip); d > 0 {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, slow down", nil)
		return
	}

	cfg := s.cfg.Load()
	if req.Username != cfg.Auth.Username || !cfg.CheckPassword(req.Password) {
		s.throttle.recordFailure(ip)
		writeError(w, http.StatusUnauthorized, "invalid username or password", nil)
		return
	}
	s.throttle.recordSuccess(ip)
	writeJSON(w, http.StatusOK, loginResponse{MustChange: cfg.Auth.MustChange})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword implements PUT /api/auth/password. It is reachable
// even while must_change is set (it's the only such route besides the
// static UI), since that flag can only be cleared by using it.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters", nil)
		return
	}

	cur := s.cfg.Load()
	if !cur.CheckPassword(req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect", nil)
		return
	}

	next := cur.Clone()
	if err := next.SetPassword(req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set password", err)
		return
	}
	if err := s.store.Save(next); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save configuration", err)
		return
	}
	s.cfg.Store(next)
	// The old password's cached Authorization headers must stop working
	// immediately, not linger for up to the cache TTL.
	s.authCache.flush()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
