package shell

import (
	"context"
	"encoding/json"
)

func (r *Runner) RunNamedContext(ctx context.Context, raw json.RawMessage) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.runNamed(ctx, raw)
}

func (r *Runner) RunArgvContext(ctx context.Context, raw json.RawMessage) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.runArgv(ctx, raw)
}
