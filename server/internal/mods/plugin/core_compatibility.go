package plugin

import (
	"context"
	"fmt"
)

// CheckCoreCompatibility verifies every installed artifact against this core's
// API/schema/capabilities. Missing artifacts fail preflight; disabling alone
// cannot erase a durable requirement. Startup still enforces every requirement.
func (m *Manager) CheckCoreCompatibility(ctx context.Context) error {
	ids, err := m.repo.installedIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		state, err := m.repo.status(ctx, id)
		if err != nil {
			return err
		}
		if state.Uninstalled {
			continue
		}
		_, r, err := m.packages.Open(ctx, state.DesiredDigest)
		if err != nil {
			return fmt.Errorf("plugin %s is incompatible with candidate core: %w", id, err)
		}
		if err := r.Close(); err != nil {
			return err
		}
	}
	return nil
}
