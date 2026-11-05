package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthLockout_EscalationAndUnlock(t *testing.T) {
	now := time.Date(2026, 11, 6, 1, 0, 0, 0, time.UTC)
	l := newAuthLockout()
	l.now = func() time.Time { return now }
	key := authKey("203.0.113.9", "management")

	for i := 0; i < authFailThreshold; i++ {
		if d, _ := l.fail(key); d != 0 {
			t.Fatalf("failure %d locked too early: %v", i+1, d)
		}
	}
	d, stage := l.fail(key)
	if d != authLockFirst || stage != 1 {
		t.Fatalf("first lock = %v stage=%d, want 10m/1", d, stage)
	}
	if got := l.lockedFor(key); got <= 0 || got > authLockFirst {
		t.Fatalf("lockedFor = %v", got)
	}
	now = now.Add(authLockFirst + time.Second)
	if got := l.lockedFor(key); got != 0 {
		t.Fatalf("lock should expire, got %v", got)
	}
	for i := 0; i < authFailThreshold; i++ {
		_, _ = l.fail(key)
	}
	d, stage = l.fail(key)
	if d != authLockSecond || stage != 2 {
		t.Fatalf("second lock = %v stage=%d, want 30m/2", d, stage)
	}
	now = now.Add(authLockSecond + time.Second)
	for i := 0; i < authFailThreshold; i++ {
		_, _ = l.fail(key)
	}
	d, stage = l.fail(key)
	if d != authLockThird || stage != 3 {
		t.Fatalf("third lock = %v stage=%d, want 24h/3", d, stage)
	}

	l.unlockIP("203.0.113.9")
	if got := l.lockedFor(key); got != 0 {
		t.Fatalf("unlock left a lock: %v", got)
	}
	for i := 0; i < authFailThreshold; i++ {
		_, _ = l.fail(key)
	}
	d, stage = l.fail(key)
	if d != authLockFirst || stage != 1 {
		t.Fatalf("after unlock = %v stage=%d, want reset 10m/1", d, stage)
	}
}

func TestRequestIP_TrustsCloudflareOnlyFromLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if got := requestIP(r); got != "203.0.113.7" {
		t.Fatalf("loopback CF-Connecting-IP = %q", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.8:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if got := requestIP(r); got != "192.168.1.8" {
		t.Fatalf("LAN request trusted spoofed header: %q", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.3, 10.0.0.1")
	if got := requestIP(r); got != "198.51.100.3" {
		t.Fatalf("loopback XFF = %q", got)
	}
}

func TestManagementWrap_IPLockoutAndUnlock(t *testing.T) {
	m := NewManagementServer(0, "secret", nil)
	h := m.wrap(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := func(ip, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		r.RemoteAddr = ip + ":4321"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h(rec, r)
		return rec
	}

	for i := 0; i < authFailThreshold; i++ {
		if rec := req("203.0.113.20", "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status=%d", i+1, rec.Code)
		}
	}
	rec := req("203.0.113.20", "wrong")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth failure = %d retry=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := req("203.0.113.20", "secret"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("correct token while locked = %d", rec.Code)
	}
	if rec := req("203.0.113.21", "secret"); rec.Code != http.StatusOK {
		t.Fatalf("different IP = %d", rec.Code)
	}
	// Missing credentials do not count as a wrong attempt.
	for i := 0; i < 10; i++ {
		if rec := req("203.0.113.22", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("missing credential status=%d", rec.Code)
		}
	}
	m.lockout().unlockIP("203.0.113.20")
	if rec := req("203.0.113.20", "secret"); rec.Code != http.StatusOK {
		t.Fatalf("after SSH unlock = %d", rec.Code)
	}
}

func TestHubAccess_RecordAndCenterUnlock(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("global-secret", true, HubClientOptions{StateFile: t.TempDir() + "/hub-devices.json", RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	_ = pairTestDevice(t, m, "node-access", "omp-access.example")

	m.recordHubAccess("node-access", "omp-access.example", "203.0.113.30", "/chat", http.StatusOK)
	m.lockout().fail(authKey("203.0.113.30", "relay"))
	rec := httptest.NewRecorder()
	m.handleHubAccess(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/access?device=node-access", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("access status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			Records []HubAccessRecord `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode access: %v", err)
	}
	if len(got.Data.Records) != 1 || got.Data.Records[0].IP != "203.0.113.30" ||
		got.Data.Records[0].Count != 1 || got.Data.Records[0].LastPath != "/chat" {
		t.Fatalf("records = %+v", got.Data.Records)
	}

	body, _ := json.Marshal(map[string]string{"ip": "203.0.113.30"})
	unlock := httptest.NewRequest(http.MethodPost, "/api/v1/hub/access/unlock", bytes.NewReader(body))
	unlockRec := httptest.NewRecorder()
	m.handleHubAccessUnlock(unlockRec, unlock)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("unlock status=%d body=%s", unlockRec.Code, unlockRec.Body.String())
	}
	if got := m.lockout().lockedFor(authKey("203.0.113.30", "relay")); got != 0 {
		t.Fatalf("relay lock remains: %v", got)
	}

	client := NewManagementServer(0, "mgmt", nil)
	if err := client.EnableHub("", false, HubClientOptions{ServerURL: "http://127.0.0.1:1", StateFile: t.TempDir() + "/hub-client.json"}); err != nil {
		t.Fatalf("EnableHub(client): %v", err)
	}
	defer client.Stop()
	clientRec := httptest.NewRecorder()
	client.handleHubAccessUnlock(clientRec, httptest.NewRequest(http.MethodPost, "/api/v1/hub/access/unlock", bytes.NewReader(body)))
	if clientRec.Code != http.StatusConflict {
		t.Fatalf("client unlock status=%d, want 409", clientRec.Code)
	}
}
