package core

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authFailThreshold = 5
	authLockFirst     = 10 * time.Minute
	authLockSecond    = 30 * time.Minute
	authLockThird     = 24 * time.Hour
)

// authLockEntry tracks one client IP. stage survives lock expiry so a client
// that keeps failing escalates 10m -> 30m -> 24h; a successful login resets it.
type authLockEntry struct {
	failures    int
	stage       int
	lockedUntil time.Time
	lastSeen    time.Time
}

type authLockout struct {
	mu      sync.Mutex
	entries map[string]*authLockEntry
	now     func() time.Time
}

func newAuthLockout() *authLockout {
	return &authLockout{entries: make(map[string]*authLockEntry), now: time.Now}
}

// lockedFor returns the remaining lock duration for ip, or 0 if not locked.
func (l *authLockout) lockedFor(ip string) time.Duration {
	if l == nil || ip == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entries[ip]
	if e == nil {
		return 0
	}
	if e.lockedUntil.After(now) {
		return e.lockedUntil.Sub(now)
	}
	// A successful login is the only immediate reset. Drop an entry that has
	// been idle for a full day so the map does not grow forever.
	if e.failures == 0 && now.Sub(e.lastSeen) > 24*time.Hour {
		delete(l.entries, ip)
	}
	return 0
}

// fail records one wrong credential. After more than authFailThreshold errors
// it returns the lock duration (10m, 30m, 24h) and the escalation stage.
func (l *authLockout) fail(ip string) (time.Duration, int) {
	if l == nil || ip == "" {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entries[ip]
	if e == nil {
		e = &authLockEntry{}
		l.entries[ip] = e
	}
	if e.lockedUntil.After(now) {
		return e.lockedUntil.Sub(now), e.stage
	}
	e.lastSeen = now
	e.failures++
	if e.failures <= authFailThreshold {
		return 0, e.stage
	}
	if e.stage < 3 {
		e.stage++
	}
	d := authLockThird
	switch e.stage {
	case 1:
		d = authLockFirst
	case 2:
		d = authLockSecond
	case 3:
		d = authLockThird
	}
	e.failures = 0
	e.lockedUntil = now.Add(d)
	return d, e.stage
}

// unlockIP removes every lock/failure for ip (management and relay scopes).
func (l *authLockout) unlockIP(ip string) {
	if l == nil || ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := ip + "|"
	for k := range l.entries {
		if k == ip || strings.HasPrefix(k, prefix) {
			delete(l.entries, k)
		}
	}
}

// success clears failures and escalation; the caller authenticated correctly.
func (l *authLockout) success(ip string) {
	if l == nil || ip == "" {
		return
	}
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

// requestIP returns the client IP. Cloudflared connects over loopback, so only
// then trust CF-Connecting-IP / X-Forwarded-For. Direct LAN/Tailscale requests
// use their socket address and cannot spoof the header.
func requestFromLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func requestIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(v) != nil {
			return v
		}
		if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
			first := strings.TrimSpace(strings.Split(v, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	if host == "" {
		return "unknown"
	}
	return host
}

func authKey(ip, scope string) string {
	return ip + "|" + scope
}

func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return strconv.Itoa(int((d + time.Second - 1) / time.Second))
}

func mgmtLocked(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", retryAfterSeconds(d))
	mgmtError(w, http.StatusTooManyRequests,
		fmt.Sprintf("too many failed attempts; try again in %s", d.Round(time.Second)))
}

func httpLocked(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", retryAfterSeconds(d))
	http.Error(w, fmt.Sprintf("too many failed login attempts; try again in %s", d.Round(time.Second)),
		http.StatusTooManyRequests)
}
