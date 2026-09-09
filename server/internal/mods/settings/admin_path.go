package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NovaWorks/zcard-next/server/internal/platform/adminpath"
)

// SetAdminBasePath sets the startup fallback before the HTTP server begins serving.
func (s *AdminSettingsService) SetAdminBasePath(base string) error {
	p, err := adminpath.Normalize(base)
	if err != nil {
		return err
	}
	if p == "" {
		p = "/admin"
	}
	s.adminBasePath = p
	return nil
}

// AdminPath reads the saved entry on each request so changes take effect immediately.
// Read errors must not reopen the default admin entry.
func (s *AdminSettingsService) AdminPath(ctx context.Context) (string, error) {
	value, err := s.uc.Get(ctx, "site", "admin_path")
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", err
	}
	var configured string
	if err == nil {
		if e := json.Unmarshal(value, &configured); e != nil {
			return "", e
		}
	}
	p, err := adminpath.Normalize(configured)
	if err != nil {
		return "", err
	}
	if p != "" {
		return p, nil
	}
	if s.adminBasePath != "" {
		return s.adminBasePath, nil
	}
	return "/admin", nil
}
