package httpapi

import (
	"net/http"
)

// handleStatic serves the embedded web UI. These files are deliberately
// unauthenticated at the HTTP layer — the UI itself holds no secrets, and
// login.html is what collects credentials in the first place. Every API
// call the JS makes carries its own Authorization header; a stolen static
// asset gains an attacker nothing.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	http.FileServer(http.FS(s.assets)).ServeHTTP(w, r)
}
