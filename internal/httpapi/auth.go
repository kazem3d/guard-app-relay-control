package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"
)

// authCache remembers recently-verified Authorization header values so a
// request doesn't pay bcrypt's cost (hundreds of ms on a Pi at the default
// bcrypt cost) on every single call. Basic auth on every trigger means an
// attacker who sends garbage credentials at speed would otherwise pin the
// CPU running bcrypt with no valid password needed at all — that's the
// specific denial-of-service this cache closes.
//
// The cache holds a hash of the *header value*, not the password, and is
// flushed whenever the password changes so a rotation takes effect
// immediately.
type authCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[[32]byte]time.Time // header hash -> verified-until
}

func newAuthCache(ttl time.Duration) *authCache {
	return &authCache{ttl: ttl, entries: make(map[[32]byte]time.Time)}
}

func (c *authCache) check(headerValue string) bool {
	key := sha256.Sum256([]byte(headerValue))
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.entries[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(c.entries, key)
		return false
	}
	return true
}

func (c *authCache) remember(headerValue string) {
	key := sha256.Sum256([]byte(headerValue))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = time.Now().Add(c.ttl)
}

func (c *authCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[[32]byte]time.Time)
}

// throttle imposes a progressive delay per source IP after repeated failed
// auth attempts, to blunt password guessing. It is intentionally small and
// in-memory: this is a single-appliance daemon, not a fleet.
type throttle struct {
	mu    sync.Mutex
	fails map[string]*failState
}

type failState struct {
	count   int
	resetAt time.Time
}

func newThrottle() *throttle {
	return &throttle{fails: make(map[string]*failState)}
}

// delay returns how long the caller should be kept waiting before their
// credentials are even checked, based on recent failures from this IP.
func (t *throttle) delay(ip string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	fs, ok := t.fails[ip]
	if !ok || time.Now().After(fs.resetAt) {
		return 0
	}
	if fs.count <= 5 {
		return 0
	}
	// 200ms per failure past the 5-failure threshold, capped at 5s.
	d := time.Duration(fs.count-5) * 200 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func (t *throttle) recordFailure(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fs, ok := t.fails[ip]
	if !ok || time.Now().After(fs.resetAt) {
		fs = &failState{}
		t.fails[ip] = fs
	}
	fs.count++
	fs.resetAt = time.Now().Add(60 * time.Second)
}

func (t *throttle) recordSuccess(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, ip)
}

// requireAuth wraps next with HTTP Basic authentication, deliberately never
// sending a WWW-Authenticate header. That header is the one thing that
// triggers a browser's native grey credential popup; omitting it means a
// 401 from the API is just a 401, and the styled login page (which attaches
// "Authorization: Basic ..." itself once the operator signs in) is what the
// browser actually shows. Machine clients are unaffected: curl -u and any
// standard HTTP client send Basic preemptively regardless of the challenge
// header.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)

		if d := s.throttle.delay(ip); d > 0 {
			time.Sleep(d)
		}

		header := r.Header.Get("Authorization")
		user, pass, ok := parseBasicAuth(header)
		if !ok {
			s.unauthorized(w)
			return
		}

		if s.authCache.check(header) {
			next(w, r)
			return
		}

		cfg := s.cfg.Load()
		validUser := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Auth.Username)) == 1
		validPass := cfg.CheckPassword(pass)
		if !validUser || !validPass {
			s.throttle.recordFailure(ip)
			s.unauthorized(w)
			return
		}

		s.throttle.recordSuccess(ip)
		s.authCache.remember(header)
		next(w, r)
	}
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	// Deliberately no WWW-Authenticate header; see requireAuth's doc comment.
	writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
}

// parseBasicAuth duplicates the decoding net/http's Request.BasicAuth does,
// without also touching WWW-Authenticate response semantics we don't want.
func parseBasicAuth(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	req := &http.Request{Header: http.Header{"Authorization": []string{header}}}
	return req.BasicAuth()
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}
	return host
}
