package marketaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
)

var ErrUnavailable = errors.New("market.DEPENDENCY_UNAVAILABLE")
var ErrUnsupported = errors.New("market.ACCOUNT_UNSUPPORTED")

type RemoteError struct {
	Status int
	Code   string
}

func (e *RemoteError) Error() string { return e.Code }
func Terminal(err error) bool {
	var e *RemoteError
	return errors.As(err, &e) && (e.Status == 401 || e.Status == 403)
}

type Client struct {
	origin string
	http   *http.Client
}

func NewClient(origin, testOrigin string) (*Client, error) {
	normalized, e := mc.Origin(origin)
	if e != nil || normalized != origin {
		return nil, ErrUnavailable
	}
	c, e := httpx.NewClient(httpx.Options{Timeout: 10 * time.Second, MaxResponseBytes: MaxResponseBytes, RequireHTTPS: true, TestOrigin: testOrigin})
	if e != nil {
		return nil, e
	}
	// No credential-bearing request ever follows a redirect, including same-origin redirects.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{origin: origin, http: c}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) Call(ctx context.Context, id, token string, input, out any) error {
	var ep Endpoint
	found := false
	for _, v := range Endpoints() {
		if v.ID == id && v.Service == "market" {
			ep = v
			found = true
			break
		}
	}
	if !found {
		return ErrInvalid
	}
	path := ep.Path
	var body []byte
	if ep.Method == http.MethodGet {
		name := ep.Request
		if _, ok := input.(PluginPath); ok {
			name = "PluginPath"
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return ErrInvalid
		}
		dst, err := NewMessage(name)
		if err != nil || Decode(name, raw, dst, MaxRequestBytes) != nil {
			return ErrInvalid
		}

		switch in := input.(type) {
		case ListingQuery:
			raw, _ := json.Marshal(in)
			var values map[string]any
			json.Unmarshal(raw, &values)
			q := url.Values{}
			for k, v := range values {
				q.Set(k, fmt.Sprint(v))
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
		case ProductQuery:
			q := url.Values{}
			if in.Cursor != "" {
				q.Set("cursor", in.Cursor)
			}
			if in.Limit != 0 {
				q.Set("limit", strconv.Itoa(in.Limit))
			}
			if in.Query != "" {
				q.Set("q", in.Query)
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
		case PageQuery:
			q := url.Values{}
			if in.Cursor != "" {
				q.Set("cursor", in.Cursor)
			}
			if in.Limit != 0 {
				q.Set("limit", strconv.Itoa(in.Limit))
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
		case PluginPath:
			raw, _ := json.Marshal(in)
			var p PluginPath
			if Decode("PluginPath", raw, &p, MaxRequestBytes) != nil {
				return ErrInvalid
			}
			path = strings.ReplaceAll(ep.Path, "{pluginId}", url.PathEscape(in.PluginID))
		case Empty:
		default:
			return ErrInvalid
		}
	} else {
		var e error
		body, e = json.Marshal(input)
		if e != nil {
			return ErrInvalid
		}
		dst, e := NewMessage(ep.Request)
		if e != nil || Decode(ep.Request, body, dst, MaxRequestBytes) != nil {
			return ErrInvalid
		}
	}
	req, e := http.NewRequestWithContext(ctx, ep.Method, c.origin+path, bytes.NewReader(body))
	if e != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if e != nil {
		return ErrUnavailable
	}
	if resp.StatusCode != 200 {
		var failure Failure
		if Decode("Failure", raw, &failure, MaxResponseBytes) == nil && ErrorStatuses()[failure.Code] == resp.StatusCode {
			return &RemoteError{Status: resp.StatusCode, Code: failure.Code}
		}
		if resp.StatusCode == 404 {
			return ErrUnsupported
		}
		return ErrUnavailable
	}
	if Decode(ep.Response, raw, out, MaxResponseBytes) != nil {
		return ErrUnavailable
	}
	return nil
}
func ParseProductQuery(raw string) (ProductQuery, error) {
	out := ProductQuery{}
	q, e := url.ParseQuery(raw)
	if e != nil {
		return out, ErrInvalid
	}
	for key, v := range q {
		if len(v) != 1 {
			return out, ErrInvalid
		}
		switch key {
		case "cursor":
			if v[0] == "" {
				return out, ErrInvalid
			}
			out.Cursor = v[0]
		case "q":
			out.Query = v[0]
		case "limit":
			n, e := strconv.Atoi(v[0])
			if e != nil || strconv.Itoa(n) != v[0] {
				return out, ErrInvalid
			}
			out.Limit = n
		default:
			return out, ErrInvalid
		}
	}
	encoded, _ := json.Marshal(out)
	var checked ProductQuery
	if Decode("ProductQuery", encoded, &checked, MaxRequestBytes) != nil {
		return out, ErrInvalid
	}
	if _, ok := q["limit"]; ok && out.Limit == 0 {
		return out, ErrInvalid
	}
	return out, nil
}

func ParseListingQuery(raw string) (ListingQuery, error) {
	var out ListingQuery
	values, e := url.ParseQuery(raw)
	if e != nil {
		return out, ErrInvalid
	}
	props := map[string]any{}
	for key, v := range values {
		if len(v) != 1 {
			return out, ErrInvalid
		}
		switch key {
		case "limit":
			n, e := strconv.Atoi(v[0])
			if e != nil || strconv.Itoa(n) != v[0] || n < 1 {
				return out, ErrInvalid
			}
			props[key] = n
		case "recommended":
			if v[0] != "true" && v[0] != "false" {
				return out, ErrInvalid
			}
			props[key] = v[0] == "true"
		default:
			props[key] = v[0]
		}
	}
	encoded, _ := json.Marshal(props)
	if Decode("ListingQuery", encoded, &out, MaxRequestBytes) != nil {
		return out, ErrInvalid
	}
	return out, nil
}
