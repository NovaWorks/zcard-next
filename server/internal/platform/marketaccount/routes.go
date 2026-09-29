package marketaccount

import "encoding/json"

// Endpoint is a contract inventory, not an HTTP guard. Implementations must
// additionally verify CSRF, ownership, session versions and persistent limits.
type Endpoint struct {
	Service          string   `json:"service"`
	Permission       string   `json:"permission"`
	ID               string   `json:"id"`
	Method           string   `json:"method"`
	Path             string   `json:"path"`
	Request          string   `json:"request"`
	Response         string   `json:"response"`
	Identities       []string `json:"identities"`
	Scope            string   `json:"scope"`
	RateClass        string   `json:"rateClass"`
	CSRF             bool     `json:"csrf"`
	PathSchema       string   `json:"pathSchema"`
	MaxRequestBytes  int      `json:"maxRequestBytes"`
	MaxResponseBytes int      `json:"maxResponseBytes"`
	Phase            string   `json:"phase"`
}
type Rate struct {
	Key           string `json:"key"`
	Limit         int    `json:"limit"`
	WindowSeconds int    `json:"windowSeconds"`
}

func Endpoints() []Endpoint { var out []Endpoint; readContract("routes.json", &out); return out }
func RateLimits() map[string][]Rate {
	var out map[string][]Rate
	readContract("rates.json", &out)
	return out
}
func ErrorStatuses() map[string]int {
	var out map[string]int
	readContract("errors.json", &out)
	return out
}
func readContract(name string, out any) {
	raw, e := ContractFiles.ReadFile(name)
	if e != nil {
		panic(e)
	}
	if e = json.Unmarshal(raw, out); e != nil {
		panic(e)
	}
}

// AcceptsIdentity only describes credential class/scope; it is not authentication.
func (e Endpoint) AcceptsIdentity(identity, scope string) bool {
	for _, k := range e.Identities {
		if k == identity {
			return identity != "account_view" || e.Scope == "" || e.Scope == scope
		}
	}
	return false
}
