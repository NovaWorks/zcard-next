// Package port exposes only the stable instance identity, not core entitlements.
package port

import "context"

type InstanceIdentity interface {
	InstanceID(context.Context) (string, error)
}
