// Package marketclient consumes the signed public distribution contract.
package marketclient

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	ml "github.com/NovaWorks/zcard-next/server/internal/platform/marketlicensed"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

type Client struct {
	origin string
	keys   map[string]ed25519.PublicKey
	http   *http.Client
	token  string
}

func New(origin, testOrigin string, keys map[string]ed25519.PublicKey) (*Client, error) {
	normalized, e := mc.Origin(origin)
	if e != nil || normalized != origin {
		return nil, fmt.Errorf("invalid market origin")
	}
	h, e := httpx.NewClient(httpx.Options{RequireHTTPS: true, TestOrigin: testOrigin, Timeout: 20 * time.Second, MaxResponseBytes: pc.MaxArchiveBytes})
	if e != nil {
		return nil, e
	}
	// Reject insecure configuration before performing any request.
	if len(origin) < 8 || origin[:8] != "https://" {
		if origin != testOrigin {
			return nil, fmt.Errorf("market requires HTTPS")
		}
	}
	copied := map[string]ed25519.PublicKey{}
	for k, v := range keys {
		copied[k] = append(ed25519.PublicKey(nil), v...)
	}
	return &Client{origin: origin, keys: copied, http: h}, nil
}
func (c *Client) WithCredential(token string) {
	c.token = token
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error {
		return fmt.Errorf("authenticated market redirects forbidden")
	}
}
func (c *Client) LicensedCatalog(ctx context.Context) (mc.Envelope, error) {
	raw, e := c.get(ctx, ml.Prefix+"/plugins", mc.MaxCatalogBytes)
	if e != nil {
		return mc.Envelope{}, e
	}
	env, e := mc.Decode(raw)
	if e == nil {
		e = ml.Verify(env, c.origin, c.keys, time.Now())
	}
	return env, e
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) get(ctx context.Context, path string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if e != nil {
		return nil, e
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return nil, fmt.Errorf("market request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("market returned HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, fmt.Errorf("market download interrupted")
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("market response size limit")
	}
	return raw, nil
}
func (c *Client) Catalog(ctx context.Context) (mc.Envelope, error) {
	capabilities, e := c.get(ctx, mc.Prefix+"/capabilities", 4096)
	if e != nil {
		return mc.Envelope{}, e
	}
	var cap mc.Capabilities
	if json.Unmarshal(capabilities, &cap) != nil || cap.APIVersion != mc.APIVersion || !cap.FreeDistribution || cap.MaxEntries != mc.MaxEntries {
		return mc.Envelope{}, fmt.Errorf("unsupported market capabilities")
	}
	raw, e := c.get(ctx, mc.Prefix+"/plugins", mc.MaxCatalogBytes)
	if e != nil {
		return mc.Envelope{}, e
	}
	v, e := mc.Decode(raw)
	if e == nil {
		e = mc.Verify(v, c.origin, c.keys, time.Now())
	}
	return v, e
}
func (c *Client) Download(ctx context.Context, v mc.Entry) ([]byte, error) {
	prefix := mc.Prefix
	if c.token != "" {
		prefix = ml.Prefix
	}
	if v.ArtifactPath != prefix+"/artifacts/"+v.Descriptor.ArchiveSHA256 {
		return nil, fmt.Errorf("invalid artifact path")
	}
	raw, e := c.get(ctx, v.ArtifactPath, pc.MaxArchiveBytes)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != v.Descriptor.ArchiveSHA256 || fmt.Sprint(len(raw)) != string(v.Descriptor.ArchiveBytes) {
		return nil, fmt.Errorf("artifact digest or size mismatch")
	}
	return raw, nil
}
