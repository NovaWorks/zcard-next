// Package marketprofile loads only locally provisioned public trust roots.
package marketprofile

import (
	"encoding/base64"
	"fmt"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	"os"
)

// Injected by the explicit official build target. Ordinary offline builds may omit it.
var EmbeddedBase64 string
var RequireOfficial string

func Load() (*ma.PublicProfile, error) {
	var raw []byte
	var e error
	if path := os.Getenv("ZCARD_MARKET_PROFILE"); path != "" {
		raw, e = os.ReadFile(path)
	} else if EmbeddedBase64 != "" {
		raw, e = base64.StdEncoding.Strict().DecodeString(EmbeddedBase64)
	}
	if e != nil {
		return nil, fmt.Errorf("cannot read local official market profile")
	}
	if len(raw) == 0 {
		if RequireOfficial == "true" {
			return nil, fmt.Errorf("official market profile required")
		}
		return nil, nil
	}
	return Parse(raw, true)
}
func Parse(raw []byte, official bool) (*ma.PublicProfile, error) {
	var p ma.PublicProfile
	if ma.Decode("PublicProfile", raw, &p, ma.MaxRequestBytes) != nil || ma.ValidateProfile(p, official) != nil {
		return nil, fmt.Errorf("invalid official market public profile")
	}
	return &p, nil
}
