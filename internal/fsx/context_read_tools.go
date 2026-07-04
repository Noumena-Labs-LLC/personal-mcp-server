package fsx

import (
	"context"
	"encoding/json"
)

func (t *Tools) SearchTextContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.searchTextContext(normalizeContext(ctx), raw)
}

func (t *Tools) FindContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.findContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONOutlineContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONOutline(raw)
}

func (t *Tools) JSONKeysContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONKeys(raw)
}

func (t *Tools) JSONGetContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONGet(raw)
}

func (t *Tools) JSONSliceContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONSlice(raw)
}

func (t *Tools) JSONSearchContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonSearchContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONValidateContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONValidate(raw)
}

func (t *Tools) JSONLInfoContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonLInfoContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONLReadContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonLReadContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONLTailContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonLTailContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONLFilterContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonLFilterContext(normalizeContext(ctx), raw)
}

func (t *Tools) JSONLValidateContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.jsonLValidateContext(normalizeContext(ctx), raw)
}

func (t *Tools) MarkdownOutlineContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.markdownOutlineContext(normalizeContext(ctx), raw)
}

func (t *Tools) MarkdownReadSectionContext(ctx context.Context, raw json.RawMessage) (any, error) {
	return t.markdownReadSectionContext(normalizeContext(ctx), raw)
}
