package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleEvents implements GET /api/events as a Server-Sent Events stream of
// relay.Event, so the UI can light up a relay's status dot and count down a
// pulse without polling. One goroutine per connected client, unwound when
// the request context is cancelled (client disconnect).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events := s.controller.Events()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			b, err := json.Marshal(ev.State)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: relay\ndata: %s\n\n", b)
			flusher.Flush()
		}
	}
}
