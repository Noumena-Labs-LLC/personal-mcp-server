package fsx

import (
	"context"
	"encoding/json"
)

func (t *Tools) SearchTextContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.SearchText(raw)
}

func (t *Tools) FindContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.Find(raw)
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

func (t *Tools) JSONSearchContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONSearch(raw)
}

func (t *Tools) JSONValidateContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONValidate(raw)
}

func (t *Tools) JSONLInfoContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONLInfo(raw)
}

func (t *Tools) JSONLReadContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONLRead(raw)
}

func (t *Tools) JSONLTailContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONLTail(raw)
}

func (t *Tools) JSONLFilterContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONLFilter(raw)
}

func (t *Tools) JSONLValidateContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.JSONLValidate(raw)
}

func (t *Tools) MarkdownOutlineContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.MarkdownOutline(raw)
}

func (t *Tools) MarkdownReadSectionContext(_ context.Context, raw json.RawMessage) (any, error) {
	return t.MarkdownReadSection(raw)
}
