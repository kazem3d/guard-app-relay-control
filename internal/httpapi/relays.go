package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/relay"
)

func relayIDFromPath(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("id"))
}

func (s *Server) writeRelayErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, relay.ErrNotFound):
		writeError(w, http.StatusNotFound, "relay not found", err)
	case errors.Is(err, relay.ErrDisabled):
		writeError(w, http.StatusConflict, "relay disabled", err)
	default:
		writeError(w, http.StatusInternalServerError, "relay operation failed", err)
	}
}

// handleTrigger implements POST /api/relay/{id}/trigger. An optional
// ?duration_ms= query parameter overrides the relay's configured default
// for this pulse only. See relay.Controller.Trigger for the coalescing
// behavior on a repeated call.
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := relayIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid relay id", err)
		return
	}

	var d time.Duration
	if v := r.URL.Query().Get("duration_ms"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms < config.MinDurationMS || ms > config.MaxDurationMS {
			writeError(w, http.StatusBadRequest, "invalid duration_ms", err)
			return
		}
		d = time.Duration(ms) * time.Millisecond
	}

	res, err := s.controller.Trigger(id, d)
	if err != nil {
		s.writeRelayErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleOn(w http.ResponseWriter, r *http.Request) {
	id, err := relayIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid relay id", err)
		return
	}
	res, err := s.controller.On(id)
	if err != nil {
		s.writeRelayErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleOff(w http.ResponseWriter, r *http.Request) {
	id, err := relayIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid relay id", err)
		return
	}
	res, err := s.controller.Off(id)
	if err != nil {
		s.writeRelayErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetRelay(w http.ResponseWriter, r *http.Request) {
	id, err := relayIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid relay id", err)
		return
	}
	st, err := s.controller.Get(id)
	if err != nil {
		s.writeRelayErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleListRelays(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.controller.List())
}

// handlePutRelays implements PUT /api/relays: replace the whole relay
// configuration. This validates and persists the new set, but does not
// hot-swap GPIO lines for a running process — a changed GPIO pin or added
// relay takes effect on the next restart, which the response makes explicit
// so the UI can tell the operator.
func (s *Server) handlePutRelays(w http.ResponseWriter, r *http.Request) {
	var relays []config.Relay
	if err := decodeJSON(r, &relays); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	cur := s.cfg.Load()
	next := cur.Clone()
	next.Relays = relays
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid relay configuration", err)
		return
	}
	if err := s.store.Save(next); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save configuration", err)
		return
	}
	s.cfg.Store(next)

	writeJSON(w, http.StatusOK, map[string]any{
		"relays":           next.Relays,
		"restart_required": true,
	})
}
