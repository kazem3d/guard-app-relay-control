package httpapi

import (
	"net/http"

	"github.com/rmto/raspberryrelay/internal/config"
)

// handleGetNetwork implements GET /api/network, reading live values from
// NetworkManager (not the stored intent, which can drift from reality if
// something changes the network outside this UI).
func (s *Server) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	if s.netcfg == nil {
		writeError(w, http.StatusServiceUnavailable, "network configuration unavailable", nil)
		return
	}
	status, err := s.netcfg.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read network status", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handlePutNetwork implements PUT /api/network. Applying a network change
// can sever the very connection carrying this request, so the response is
// sent before the change is applied, and the change is guarded by a
// rollback timer disarmed only by a subsequent POST
// /api/network/confirm reaching the (possibly now-different) address. See
// internal/netcfg for the rollback implementation.
func (s *Server) handlePutNetwork(w http.ResponseWriter, r *http.Request) {
	if s.netcfg == nil {
		writeError(w, http.StatusServiceUnavailable, "network configuration unavailable", nil)
		return
	}

	var intent config.NetworkIntent
	if err := decodeJSON(r, &intent); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	cur := s.cfg.Load()
	next := cur.Clone()
	next.Network = intent
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid network configuration", err)
		return
	}
	if err := s.store.Save(next); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save configuration", err)
		return
	}
	s.cfg.Store(next)

	if err := s.netcfg.Apply(intent); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply network configuration", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "applying",
		"note":    "network settings are being applied; reconnect at the new address and POST /api/network/confirm within 60s or the previous settings will be restored",
		"network": intent,
	})
}

// handleConfirmNetwork implements POST /api/network/confirm, disarming the
// rollback timer armed by handlePutNetwork.
func (s *Server) handleConfirmNetwork(w http.ResponseWriter, r *http.Request) {
	if s.netcfg == nil {
		writeError(w, http.StatusServiceUnavailable, "network configuration unavailable", nil)
		return
	}
	s.netcfg.Confirm()
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}
