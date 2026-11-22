// Package upstreamsource builds private administrator navigation metadata. It
// never contains API credentials and must not be used in customer/supply DTOs.
package upstreamsource

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

type Input struct {
	ConnectionID                            uint64
	Name, Driver, BaseURL, Code, SKU, Stage string
	Settings                                map[string]any
}

var numeric = regexp.MustCompile(`^[0-9]+$`)

// Resolve only knows verified storefront routes. Other drivers keep a website
// link and copyable IDs until an operator supplies a product URL template.
func Resolve(in Input) map[string]any {
	kind := "upstream"
	if in.ConnectionID == 0 {
		kind = "self"
	}
	product, sku := in.Code, in.SKU
	if in.Driver == "mcy_shop" {
		parts := strings.Split(in.Code, "~")
		if len(parts) == 2 && numeric.MatchString(parts[0]) && numeric.MatchString(parts[1]) {
			product, sku = parts[0], parts[1]
		}
	}
	website := Website(in.BaseURL)
	if configured, _ := in.Settings["website_url"].(string); configured != "" {
		website = cleanRoot(configured)
	}
	out := map[string]any{"version": "1", "kind": kind, "stage": in.Stage,
		"connection_id": strconv.FormatUint(in.ConnectionID, 10), "connection_name": in.Name,
		"driver": in.Driver, "product_code": in.Code, "product_id": product, "sku_id": sku,
		"website_url": website, "product_url": "", "link_status": "website_only"}
	if kind == "self" {
		out["link_status"] = "self"
		return out
	}
	template, _ := in.Settings["product_url_template"].(string)
	if template == "" {
		switch in.Driver {
		case "zcard":
			template = "{base}/product/{product_id}"
		case "mcy_shop":
			if numeric.MatchString(product) {
				template = "{base}/item?id={product_id}"
			}
		}
	}
	if website != "" && product != "" && template != "" {
		if link := expand(template, website, in.Code, product, sku); link != "" {
			out["product_url"], out["link_status"] = link, "product"
		} else {
			out["link_status"] = "invalid_template"
		}
	}
	if website == "" {
		out["link_status"] = "unavailable"
	}
	return out
}

func cleanRoot(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return strings.TrimRight(u.String(), "/")
}

// Website removes only known API suffixes, retaining installations in subpaths.
func Website(raw string) string {
	root := cleanRoot(raw)
	for _, suffix := range []string{"/api/supply", "/api/v2", "/api"} {
		if strings.HasSuffix(root, suffix) {
			return strings.TrimSuffix(root, suffix)
		}
	}
	return root
}

func expand(template, base, code, product, sku string) string {
	value := strings.ReplaceAll(strings.TrimSpace(template), "{base}", base)
	for key, raw := range map[string]string{"code": code, "product_id": product, "sku_id": sku} {
		for strings.Contains(value, "{"+key+"}") {
			i := strings.Index(value, "{"+key+"}")
			prefix := value[:i]
			// Placeholders must not control the scheme, credentials or hostname.
			start := strings.Index(value, "://")
			if start < 0 || i < start+3 || !strings.ContainsAny(value[start+3:i], "/?") {
				return ""
			}
			escaped := url.PathEscape(raw)
			if strings.Contains(prefix, "?") {
				escaped = url.QueryEscape(raw)
			}
			value = prefix + escaped + value[i+len(key)+2:]
		}
	}
	if strings.ContainsAny(value, "{}") {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return ""
	}
	u.User, u.Fragment = nil, ""
	q := u.Query()
	for key := range q {
		k := strings.ToLower(key)
		if strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "api_key") || k == "key" || k == "apikey" || k == "sign" || k == "signature" || k == "app_key" || k == "access_key" || k == "authorization" || k == "auth" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func Proto(source map[string]any) *structpb.Struct {
	if len(source) == 0 {
		return nil
	}
	out, _ := structpb.NewStruct(source)
	return out
}
