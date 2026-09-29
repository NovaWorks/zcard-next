// Package marketbinding is the instance pairing and credential protocol.
package marketbinding

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
	"io"
	"net/http"
	"regexp"
	"time"
)

const Prefix = "/api/market/v1/binding"
const PollSeconds = 5
const PairLifetime = 10 * time.Minute
const CredentialLifetime = 30 * 24 * time.Hour

var opaque = regexp.MustCompile(`^[0-9a-f]{64}$`)
var identity = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func ValidSecret(s string) bool   { return opaque.MatchString(s) }
func ValidInstance(s string) bool { return identity.MatchString(s) }
func Random() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}

type Start struct {
	RequestID   string `json:"requestId"`
	StartSecret string `json:"startSecret"`
	InstanceID  string `json:"instanceId"`
}
type Pair struct {
	PairID       string `json:"pairId"`
	DeviceSecret string `json:"deviceSecret"`
	UserCode     string `json:"userCode"`
	ExpiresAt    int64  `json:"expiresAt"`
	PollSeconds  int    `json:"pollSeconds"`
}
type Proof struct {
	PairID       string `json:"pairId"`
	DeviceSecret string `json:"deviceSecret"`
	AccountID    string `json:"accountId,omitempty"`
}
type PairStatus struct {
	Status      string `json:"status"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	ExpiresAt   int64  `json:"expiresAt"`
}
type Credential struct {
	Token       string `json:"token"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	Version     string `json:"version"`
	ExpiresAt   int64  `json:"expiresAt"`
}
type Operation struct {
	RequestID string `json:"requestId"`
}
type Approval struct {
	UserCode string `json:"userCode"`
	Confirm  bool   `json:"confirm"`
}
type ApprovalView struct {
	InstanceID string `json:"instanceId"`
	ExpiresAt  int64  `json:"expiresAt"`
}
type Sync struct {
	Licenses    []pl.Envelope           `json:"licenses"`
	Revocations []pl.RevocationEnvelope `json:"revocations,omitempty"`
}
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("market binding HTTP %d", e.Status) }

type Client struct {
	origin string
	http   *http.Client
}

func New(origin, testOrigin string) (*Client, error) {
	normalized, e := mc.Origin(origin)
	if e != nil || normalized != origin {
		return nil, fmt.Errorf("invalid market origin")
	}
	h, e := httpx.NewClient(httpx.Options{RequireHTTPS: true, TestOrigin: testOrigin, Timeout: 20 * time.Second, MaxResponseBytes: 2 << 20})
	if e != nil {
		return nil, e
	}
	if len(origin) < 8 || origin[:8] != "https://" {
		if origin != testOrigin {
			return nil, fmt.Errorf("market requires HTTPS")
		}
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return fmt.Errorf("binding redirects forbidden") }
	return &Client{origin: origin, http: h}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) Call(ctx context.Context, path, token string, in, out any) error {
	// Paths are chosen by the host, never received from a remote response.
	switch path {
	case "start", "poll", "confirm", "rotate", "revoke", "sync", "safety":
	default:
		return fmt.Errorf("invalid binding operation")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+Prefix+"/"+path, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return fmt.Errorf("market binding request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &HTTPError{resp.StatusCode}
	}
	raw, e = io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if e != nil {
		return fmt.Errorf("market binding response interrupted")
	}
	return pl.DecodeLimit(raw, out, 2<<20)
}
