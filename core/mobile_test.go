package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newMobileTestServer(t *testing.T, path string) (*ManagementServer, *httptest.Server) {
	t.Helper()
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{StateFile: path + ".hub", RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	if err := m.EnableMobile(MobileOptions{StateFile: path, RequireConsole: true, RequireForKeys: true}); err != nil {
		t.Fatalf("EnableMobile: %v", err)
	}
	srv := httptest.NewServer(m.buildHandler(http.NewServeMux()))
	t.Cleanup(srv.Close)
	return m, srv
}

func mobileEnroll(t *testing.T, srv *httptest.Server, name string) (string, string) {
	t.Helper()
	body := []byte(`{"name":"` + name + `"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/mobile/enroll", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer mgmt")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("enroll status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			ID     string `json:"device_id"`
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("enroll decode: %v", err)
	}
	if out.Data.ID == "" || out.Data.Secret == "" {
		t.Fatalf("enroll returned empty id/secret: %+v", out)
	}
	return out.Data.ID, out.Data.Secret
}

func mobileSignedRequest(t *testing.T, method, url, id, secret string, body []byte) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set(mobileDeviceHeader, id)
	req.Header.Set(mobileTSHeader, ts)
	req.Header.Set(mobileSigHeader, mobileSign(secret, mobileCanonical(method, req.URL.RequestURI(), ts, body)))
	return req
}

// TestMobileEnrollAndSignature covers the base contract: enrollment returns a
// secret, a correctly signed request is accepted, and the same request cannot
// be replayed.
func TestMobileEnrollAndSignature(t *testing.T) {
	_, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	id, secret := mobileEnroll(t, srv, "Pixel")

	do := func() int {
		req := mobileSignedRequest(t, http.MethodGet, srv.URL+"/api/v1/mobile/approvals", id, secret, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("signed request: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("signed request status = %d, want 200", code)
	}
	// Identical signature (same timestamp/sig) must be rejected as a replay.
	req := mobileSignedRequest(t, http.MethodGet, srv.URL+"/api/v1/mobile/status", id, secret, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signed request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	replay, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("replay request: %v", err)
	}
	defer replay.Body.Close()
	_, _ = io.Copy(io.Discard, replay.Body)
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed request status = %d, want 401", replay.StatusCode)
	}
}

// TestMobileEnroll_SecondPhoneWithTokenFromRemote is a regression test: a
// replacement phone must be able to enroll with the management token from a
// non-loopback address while other phones are already enrolled. Previously
// this was rejected with 403, leaving a fresh install unable to enroll unless
// it borrowed an enrolled phone or reached loopback.
func TestMobileEnroll_SecondPhoneWithTokenFromRemote(t *testing.T) {
	m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	mobileEnroll(t, srv, "First")

	h := m.buildHandler(http.NewServeMux())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mobile/enroll", bytes.NewReader([]byte(`{"name":"Second"}`)))
	req.RemoteAddr = "100.115.58.60:41234"
	req.Header.Set("Authorization", "Bearer mgmt")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote token enroll status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID     string `json:"device_id"`
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Data.ID == "" || out.Data.Secret == "" {
		t.Fatalf("enroll returned empty id/secret: %s", rec.Body.String())
	}
	if got := m.mobile.deviceCount(); got != 2 {
		t.Fatalf("device count = %d, want 2", got)
	}
}

// TestMobileKeyGuardRequiresPhone guards the "phone is the second key" rule:
// once a phone is enrolled the management token alone cannot pair a node.
func TestMobileKeyGuardRequiresPhone(t *testing.T) {
	_, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	id, secret := mobileEnroll(t, srv, "Pixel")

	post := func(req *http.Request) (int, string) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	tokenOnly, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/hub/pair/new", strings.NewReader(`{"name":"pc2","host":"omp-pc2.example.com"}`))
	tokenOnly.Header.Set("Authorization", "Bearer mgmt")
	if code, body := post(tokenOnly); code != http.StatusForbidden {
		t.Fatalf("token-only key op status = %d, want 403 (%s)", code, body)
	}
	signed := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/hub/pair/new", id, secret, []byte(`{"name":"pc2","host":"omp-pc2.example.com"}`))
	if code, body := post(signed); code != http.StatusOK {
		t.Fatalf("signed key op status = %d, want 200 (%s)", code, body)
	}
}

// TestMobileGateApprovalFlow covers the browser flow end-to-end: the node page
// shows the waiting screen, the phone approves, the one-time grant link sets an
// access cookie, and the page then reaches the node.
func TestMobileGateApprovalFlow(t *testing.T) {
	m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	m.hub.mu.Lock()
	m.hub.paired["tok1"] = &HubDeviceRecord{Token: "tok1", Name: "pc1", Host: "omp-pc1.lizeyu.win"}
	m.hub.hostIndex["omp-pc1.lizeyu.win"] = "tok1"
	m.hub.mu.Unlock()
	id, secret := mobileEnroll(t, srv, "Pixel")

	relay := &hubRelay{m: m}
	call := func(req *http.Request) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		relay.handle(rr, req)
		return rr
	}

	page := call(httptest.NewRequest(http.MethodGet, "http://omp-pc1.lizeyu.win/", nil))
	if page.Code != http.StatusUnauthorized {
		t.Fatalf("gate page status = %d, want 401", page.Code)
	}
	match := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(page.Body.String())
	if match == nil {
		t.Fatalf("gate page has no nonce: %s", page.Body.String())
	}
	approvalID := match[1]

	// Unknown nonce must not leak approval state.
	bad := call(httptest.NewRequest(http.MethodGet, "http://omp-pc1.lizeyu.win/api/v1/mobile/approvals/status?nonce=nope", nil))
	if bad.Code != http.StatusNotFound {
		t.Fatalf("unknown nonce status = %d, want 404", bad.Code)
	}

	decideBody := []byte(`{"approve":true}`)
	decide := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/approvals/"+approvalID, id, secret, decideBody)
	resp, err := http.DefaultClient.Do(decide)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	defer resp.Body.Close()
	var decided struct {
		Data struct {
			Status string `json:"status"`
			Link   string `json:"link"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decided); err != nil {
		t.Fatalf("decide decode: %v", err)
	}
	if decided.Data.Status != "approved" || decided.Data.Link == "" {
		t.Fatalf("decide = %+v, want approved + link", decided)
	}

	grant := call(httptest.NewRequest(http.MethodGet, "http://omp-pc1.lizeyu.win"+decided.Data.Link, nil))
	if grant.Code != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303 (%s)", grant.Code, grant.Body.String())
	}
	cookies := grant.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("grant set no cookie")
	}

	pass := httptest.NewRequest(http.MethodGet, "http://omp-pc1.lizeyu.win/", nil)
	for _, c := range cookies {
		pass.AddCookie(c)
	}
	after := call(pass)
	if after.Code == http.StatusUnauthorized {
		t.Fatalf("page still gated after approval: %s", after.Body.String())
	}

	// The grant link is single-use.
	again := call(httptest.NewRequest(http.MethodGet, "http://omp-pc1.lizeyu.win"+decided.Data.Link, nil))
	if again.Code != http.StatusGone {
		t.Fatalf("second grant use status = %d, want 410", again.Code)
	}
}

// TestMobileConsoleGateSparesMachineEndpoints is the regression for the console
// gate breaking hub nodes: a node reconnect to /api/v1/hub/tunnel must reach
// the tunnel handler (which wants X-Hub-Device), not the browser approval page,
// otherwise every retry queues another approval and the node never comes back.
func TestMobileConsoleGateSparesMachineEndpoints(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	dir := t.TempDir()
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{StateFile: dir + "/hub.json", RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	if err := m.EnableMobile(MobileOptions{StateFile: dir + "/mobile.json", RequireConsole: true, RequireForKeys: true}); err != nil {
		t.Fatalf("EnableMobile: %v", err)
	}
	srv := httptest.NewServer(m.mobileConsoleGate(m.buildHandler(http.NewServeMux())))
	t.Cleanup(srv.Close)
	mobileEnroll(t, srv, "Pixel")

	do := func(method, path string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(raw)
	}

	if code, body := do(http.MethodGet, "/api/v1/hub/tunnel"); code != http.StatusBadRequest {
		t.Fatalf("tunnel through gate = %d (%s), want the tunnel handler's 400", code, body)
	}
	if code, body := do(http.MethodPost, "/api/v1/hub/pair/claim"); code == http.StatusUnauthorized && strings.Contains(body, "nonce") {
		t.Fatalf("pair/claim hit the approval page: %d (%s)", code, body)
	}

	m.mobile.mu.Lock()
	pending := len(m.mobile.approvals)
	m.mobile.mu.Unlock()
	if pending != 0 {
		t.Fatalf("machine endpoints queued %d approvals, want 0", pending)
	}

	// Console-scoped browser traffic is still gated.
	if code, _ := do(http.MethodGet, "/api/v1/hub/nodes"); code != http.StatusUnauthorized {
		t.Fatalf("console api through gate = %d, want 401", code)
	}
	m.mobile.mu.Lock()
	pending = len(m.mobile.approvals)
	m.mobile.mu.Unlock()
	if pending != 1 {
		t.Fatalf("gated console api queued %d approvals, want 1", pending)
	}
}

// TestMobileConsoleGateApprovalFlow is the regression for the console hostname
// being public again: a browser on the management console must be approved on
// the phone before it sees the SPA or any management API, while the signed app
// API keeps working so the phone can actually approve.
func TestMobileConsoleGateApprovalFlow(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	dir := t.TempDir()
	if err := m.EnableMobile(MobileOptions{StateFile: dir + "/mobile.json", RequireConsole: true, RequireForKeys: true}); err != nil {
		t.Fatalf("EnableMobile: %v", err)
	}
	srv := httptest.NewServer(m.mobileConsoleGate(m.buildHandler(http.NewServeMux())))
	t.Cleanup(srv.Close)

	id, secret := mobileEnroll(t, srv, "Pixel")

	// The SPA and the token API are both behind the gate.
	page, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	raw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusUnauthorized {
		t.Fatalf("console status = %d, want 401 (%s)", page.StatusCode, raw)
	}
	match := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("gate page has no nonce: %s", raw)
	}
	approvalID := match[1]

	// The app API stays usable without approval, otherwise the phone could not
	// list or decide anything.
	status := mobileSignedRequest(t, http.MethodGet, srv.URL+"/api/v1/mobile/status", id, secret, nil)
	resp, err := http.DefaultClient.Do(status)
	if err != nil {
		t.Fatalf("mobile status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signed status through gate = %d, want 200", resp.StatusCode)
	}

	decideBody := []byte(`{"approve":true}`)
	decide := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/approvals/"+approvalID, id, secret, decideBody)
	resp, err = http.DefaultClient.Do(decide)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	var decided struct {
		Data struct {
			Status string `json:"status"`
			Link   string `json:"link"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&decided)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("decide decode: %v", err)
	}
	if decided.Data.Status != "approved" || decided.Data.Link == "" {
		t.Fatalf("decide = %+v, want approved + link", decided)
	}

	// The grant answers with a redirect to "/"; follow it manually so the
	// assertion sees the grant itself rather than the gated page behind it.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	grant, err := noRedirect.Get(srv.URL + decided.Data.Link)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	grant.Body.Close()
	if grant.StatusCode != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303", grant.StatusCode)
	}
	var access *http.Cookie
	for _, c := range grant.Cookies() {
		if c.Name == mobileAccessCookie {
			access = c
		}
	}
	if access == nil {
		t.Fatalf("grant set no %s cookie", mobileAccessCookie)
	}
	d := m.mobile
	d.mu.Lock()
	acc := d.access[access.Value]
	d.mu.Unlock()
	if acc == nil || acc.Node != mobileConsoleNode {
		t.Fatalf("grant scoped to %+v, want node %q", acc, mobileConsoleNode)
	}

	// A valid management token alone no longer reaches the API from a browser
	// without the phone grant; the same request passes once the cookie is set.
	browserAPI := func(cookie *http.Cookie) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/projects", nil)
		req.Header.Set("Authorization", "Bearer mgmt")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("console api: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := browserAPI(nil); code != http.StatusUnauthorized {
		t.Fatalf("token-only console api = %d, want 401", code)
	}
	if code := browserAPI(access); code != http.StatusOK {
		t.Fatalf("approved console api = %d, want 200", code)
	}
}

// TestMobileAccessGrantURLOpen grants a node URL directly from the app.
func TestMobileAccessGrantURLOpen(t *testing.T) {
	m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	m.hub.mu.Lock()
	m.hub.paired["tok1"] = &HubDeviceRecord{Token: "tok1", Name: "pc1", Host: "omp-pc1.lizeyu.win"}
	m.hub.hostIndex["omp-pc1.lizeyu.win"] = "tok1"
	m.hub.mu.Unlock()
	id, secret := mobileEnroll(t, srv, "Pixel")

	body := []byte(`{"device":"tok1"}`)
	req := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/access/grant", id, secret, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("grant status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("grant decode: %v", err)
	}
	if !strings.HasPrefix(out.Data.URL, "https://omp-pc1.lizeyu.win/__omp/grant?t=") {
		t.Fatalf("grant url = %q", out.Data.URL)
	}
}

// TestMobileAccessGrantURL_Console grants the management console itself. The
// console is not a paired node, so the link must use the origin the app called
// in on, and redeeming it must carry the browser past the console gate.
func TestMobileAccessGrantURL_Console(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	dir := t.TempDir()
	if err := m.EnableMobile(MobileOptions{StateFile: dir + "/mobile.json", RequireConsole: true, RequireForKeys: true}); err != nil {
		t.Fatalf("EnableMobile: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "console")
	})
	srv := httptest.NewServer(m.mobileConsoleGate(m.buildHandler(mux)))
	t.Cleanup(srv.Close)
	id, secret := mobileEnroll(t, srv, "Pixel")

	req := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/access/grant", id, secret, []byte(`{"device":"console"}`))
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("grant status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			URL    string `json:"url"`
			Device string `json:"device"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("grant decode: %v", err)
	}
	if out.Data.Device != mobileConsoleNode {
		t.Fatalf("grant device = %q, want %q", out.Data.Device, mobileConsoleNode)
	}
	// X-Forwarded-Proto is only honoured for the scheme, not the host: the
	// link must still point at the origin the app reached us on.
	want := "https://" + strings.TrimPrefix(srv.URL, "http://") + "/__omp/grant?t="
	if !strings.HasPrefix(out.Data.URL, want) {
		t.Fatalf("grant url = %q, want prefix %q", out.Data.URL, want)
	}

	// Redeem it over http: the cookie is host-scoped, so the https rewrite in
	// the link only matters for real browsers behind the tunnel.
	link := strings.Replace(out.Data.URL, "https://", "http://", 1)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	redeem, err := client.Get(link)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	raw, _ := io.ReadAll(redeem.Body)
	redeem.Body.Close()
	if redeem.StatusCode != http.StatusOK || string(raw) != "console" {
		t.Fatalf("redeem = %d %q, want 200 console (grant must clear the gate)", redeem.StatusCode, raw)
	}
}

// TestMobileAccessGrantURL_TailnetUsesHTTPS// TestMobileAccessGrantURL_TailnetUsesHTTPS is the regression for the phone
// opening nodes at http://<name>.ts.net (port 80, refused) while the relay only
// serves the tailnet front door over TLS. The grant must use the recorded
// web_url, and the host fallback must not treat tailnet names as plain http.
func TestMobileAccessGrantURL_TailnetUsesHTTPS(t *testing.T) {
	for _, tc := range []struct {
		name   string
		webURL string
	}{
		{name: "recorded web url", webURL: "https://home-ubuntu.tail7fe07c.ts.net"},
		{name: "legacy record without web url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
			m.hub.mu.Lock()
			m.hub.paired["tok1"] = &HubDeviceRecord{
				Token:  "tok1",
				Name:   "lct950915",
				Host:   "home-ubuntu.tail7fe07c.ts.net",
				WebURL: tc.webURL,
			}
			m.hub.hostIndex["home-ubuntu.tail7fe07c.ts.net"] = "tok1"
			m.hub.mu.Unlock()
			id, secret := mobileEnroll(t, srv, "Pixel")

			req := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/access/grant", id, secret, []byte(`{"device":"tok1"}`))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("grant: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("grant status %d: %s", resp.StatusCode, raw)
			}
			var out struct {
				Data struct {
					URL string `json:"url"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("grant decode: %v", err)
			}
			if !strings.HasPrefix(out.Data.URL, "https://home-ubuntu.tail7fe07c.ts.net/__omp/grant?t=") {
				t.Fatalf("grant url = %q", out.Data.URL)
			}
		})
	}
}

func TestMobileNodeBaseURL(t *testing.T) {
	cases := map[string]string{
		"home-ubuntu.tail7fe07c.ts.net": "https://home-ubuntu.tail7fe07c.ts.net",
		"omp-pc1.lizeyu.win":            "https://omp-pc1.lizeyu.win",
		"127.0.0.1:30177":               "http://127.0.0.1:30177",
		"localhost:30177":               "http://localhost:30177",
		"100.108.43.107:31990":          "http://100.108.43.107:31990",
	}
	for host, want := range cases {
		if got := mobileNodeBaseURL(host); got != want {
			t.Errorf("mobileNodeBaseURL(%q) = %q, want %q", host, got, want)
		}
	}
}

// TestMobileRevokeRemovesAccess ensures revoking a phone also drops its live
// browser grants.
func TestMobileRevokeRemovesAccess(t *testing.T) {
	m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	id, secret := mobileEnroll(t, srv, "Pixel")
	m.mobile.mu.Lock()
	m.mobile.access["cookie1"] = &mobileAccess{Node: "tok1", Device: id, ExpiresAt: time.Now().Add(time.Hour)}
	m.mobile.mu.Unlock()

	req := mobileSignedRequest(t, http.MethodPost, srv.URL+"/api/v1/mobile/devices/"+id+"/revoke", id, secret, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", resp.StatusCode)
	}
	m.mobile.mu.Lock()
	_, keptAccess := m.mobile.access["cookie1"]
	_, keptDevice := m.mobile.devices[id]
	m.mobile.mu.Unlock()
	if keptAccess || keptDevice {
		t.Fatalf("revoke left device=%v access=%v", keptDevice, keptAccess)
	}
}

// TestMobileRelayFallbackSharedOrigin covers a single front door (one
// tailnet DNS name) in front of many nodes. The Host header no longer names a
// paired node, so the relay must resolve the node from the grant link, the
// access cookie, and the gate page's status poll nonce.
func TestMobileRelayFallbackSharedOrigin(t *testing.T) {
	m, srv := newMobileTestServer(t, t.TempDir()+"/mobile.json")
	m.hub.mu.Lock()
	m.hub.paired["tok1"] = &HubDeviceRecord{Token: "tok1", Name: "pc1", Host: "omp-pc1.lizeyu.win"}
	m.hub.hostIndex["omp-pc1.lizeyu.win"] = "tok1"
	m.hub.mu.Unlock()
	mobileEnroll(t, srv, "Pixel")

	const shared = "http://home-ubuntu.tail7fe07c.ts.net"
	relay := &hubRelay{m: m}
	call := func(req *http.Request) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		relay.handle(rr, req)
		return rr
	}

	// With a single paired node the gate page can still be served here.
	if code := call(httptest.NewRequest(http.MethodGet, shared+"/", nil)).Code; code != http.StatusUnauthorized {
		t.Fatalf("bare shared origin status = %d, want 401", code)
	}

	// The gate page at the shared origin must reach the node's approval.
	page := call(httptest.NewRequest(http.MethodGet, shared+"/", nil))
	if page.Code != http.StatusUnauthorized {
		t.Fatalf("gate page status = %d, want 401", page.Code)
	}
	match := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(page.Body.String())
	if match == nil {
		t.Fatalf("gate page has no nonce: %s", page.Body.String())
	}
	status := call(httptest.NewRequest(http.MethodGet, shared+"/api/v1/mobile/approvals/status?nonce="+match[1], nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"pending"`) {
		t.Fatalf("status poll = %d %s, want 200 pending", status.Code, status.Body.String())
	}

	// A grant link issued for the node opens the node at the shared origin.
	m.mobile.mu.Lock()
	link := randToken()
	m.mobile.links[link] = &mobileLink{Token: link, Node: "tok1", ExpiresAt: time.Now().Add(time.Minute)}
	m.mobile.mu.Unlock()
	grant := call(httptest.NewRequest(http.MethodGet, shared+"/__omp/grant?t="+link, nil))
	if grant.Code != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303 (%s)", grant.Code, grant.Body.String())
	}
	cookies := grant.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("grant set no cookie")
	}

	// The cookie keeps the node attached on later requests from the same origin.
	pass := httptest.NewRequest(http.MethodGet, shared+"/", nil)
	for _, c := range cookies {
		pass.AddCookie(c)
	}
	if code := call(pass).Code; code == http.StatusUnauthorized || code == http.StatusNotFound {
		t.Fatalf("cookie did not resolve node, status = %d", code)
	}

	// Two paired nodes behind one origin are ambiguous without grant material,
	// but an explicit grant link still names its node.
	m.hub.mu.Lock()
	m.hub.paired["tok2"] = &HubDeviceRecord{Token: "tok2", Name: "pc2", Host: "omp-pc2.lizeyu.win"}
	m.hub.hostIndex["omp-pc2.lizeyu.win"] = "tok2"
	m.hub.mu.Unlock()
	if code := call(httptest.NewRequest(http.MethodGet, shared+"/", nil)).Code; code != http.StatusNotFound {
		t.Fatalf("ambiguous shared origin status = %d, want 404", code)
	}
	link2 := randToken()
	m.mobile.mu.Lock()
	m.mobile.links[link2] = &mobileLink{Token: link2, Node: "tok2", ExpiresAt: time.Now().Add(time.Minute)}
	m.mobile.mu.Unlock()
	if code := call(httptest.NewRequest(http.MethodGet, shared+"/__omp/grant?t="+link2, nil)).Code; code != http.StatusSeeOther {
		t.Fatalf("explicit grant link status = %d, want 303", code)
	}
}
