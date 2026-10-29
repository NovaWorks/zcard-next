package port

import "context"

type socialCapabilityKey struct{}

func WithSocialCapability(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, socialCapabilityKey{}, enabled)
}

func HasSocialCapability(ctx context.Context) bool {
	enabled, _ := ctx.Value(socialCapabilityKey{}).(bool)
	return enabled
}
