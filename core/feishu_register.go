package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Feishu/Lark account-service (RFC 8628 device-authorization) client. This is
// how a Feishu/Lark bot is bound via QR scan: the user scans the verification
// URL, authorizes in the Feishu client, and the registration poll returns a
// fresh app_id + app_secret the bridge then configures. See
// github.com/larksuite/node-sdk scene/registration.

const (
	feishuAccountsBase = "https://accounts.feishu.cn"
	larkAccountsBase   = "https://accounts.larksuite.com"
	feishuRegisterPath = "/oauth/v1/app/registration"
)

type feishuRegisterBegin struct {
	DeviceCode                string `json:"device_code"`
	VerificationURIComplete   string `json:"verification_uri_complete"`
	Interval                  int    `json:"interval"`
	ExpiresIn                 int    `json:"expires_in"`
}

type feishuRegisterPoll struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	UserOpenID      string `json:"open_id"`
	TenantBrand     string `json:"tenant_brand"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

// feishuRegisterRoundtrip posts an action to the accounts service and decodes a
// uniform JSON envelope. service is one of "feishu" or "lark".
func feishuRegisterRoundtrip(ctx context.Context, service, action string, params url.Values) ([]byte, error) {
	base := feishuAccountsBase
	if strings.EqualFold(service, "lark") {
		base = larkAccountsBase
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("action", action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+feishuRegisterPath, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("accounts service call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read accounts response: %w", err)
	}
	// RFC 8628 errors (authorization_pending / slow_down) arrive as HTTP 400
	// with a JSON body; the service is otherwise HTTP 200. Either way we hand
	// the JSON back to the caller to interpret.
	return body, nil
}

// beginFeishuRegistration starts a QR binding flow and returns the QR code URL
// plus the device_code to poll. appId, when non-empty (e.g. an existing bound
// bot), makes the flow update that app instead of creating a new one.
func beginFeishuRegistration(ctx context.Context, service, source, appID string) (*feishuRegisterBegin, string, error) {
	params := url.Values{
		"archetype":           {"PersonalAgent"},
		"auth_method":         {"client_secret"},
		"request_user_info":   {"open_id"},
	}
	raw, err := feishuRegisterRoundtrip(ctx, service, "begin", params)
	if err != nil {
		return nil, "", err
	}
	var begin feishuRegisterBegin
	if err := json.Unmarshal(raw, &begin); err != nil {
		return nil, "", fmt.Errorf("parse begin response: %w", err)
	}
	if begin.DeviceCode == "" || begin.VerificationURIComplete == "" {
		return nil, "", fmt.Errorf("accounts service returned an incomplete registration")
	}
	qr, err := url.Parse(begin.VerificationURIComplete)
	if err != nil {
		return nil, "", fmt.Errorf("parse verification URL: %w", err)
	}
	q := qr.Query()
	q.Set("from", "sdk")
	if source != "" {
		q.Set("source", source)
	}
	q.Set("tp", "sdk")
	if appID != "" {
		q.Set("clientID", appID)
	}
	qr.RawQuery = q.Encode()
	return &begin, qr.String(), nil
}

// pollFeishuRegistration checks one step of a pending QR registration. A
// successful (complete) registration returns client_id/client_secret.
func pollFeishuRegistration(ctx context.Context, service, deviceCode string) (*feishuRegisterPoll, error) {
	params := url.Values{"device_code": {deviceCode}}
	raw, err := feishuRegisterRoundtrip(ctx, service, "poll", params)
	if err != nil {
		return nil, err
	}
	var poll feishuRegisterPoll
	if err := json.Unmarshal(raw, &poll); err != nil {
		return nil, fmt.Errorf("parse poll response: %w", err)
	}
	return &poll, nil
}
