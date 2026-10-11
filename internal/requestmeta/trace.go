package requestmeta

import (
	"context"
	"github.com/google/uuid"
)

type traceKey struct{}

func WithTrace(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}
func Trace(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}
func EnsureTrace(ctx context.Context) context.Context {
	if Trace(ctx) != "" {
		return ctx
	}
	return WithTrace(ctx, uuid.NewString())
}
