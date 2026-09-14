package httpapi

import (
	"net/http"

	"github.com/rmto/raspberryrelay/internal/sysinfo"
)

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sysinfo.Collect(s.version))
}
