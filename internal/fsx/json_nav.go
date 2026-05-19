package fsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type jsonPathArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Pointer  string `json:"pointer"`
}

type JSONOutlineArgs struct {
	Path        string `json:"path"`
	Cwd         string `json:"cwd"`
	PathMode    string `json:"path_mode"`
	Pointer     string `json:"pointer"`
	MaxDepth    int    `json:"max_depth"`
	MaxChildren int    `json:"max_children"`
}

type JSONKeysArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Pointer  string `json:"pointer"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

type JSONSliceArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Pointer  string `json:"pointer"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

type JSONSearchArgs struct {
	Path          string   `json:"path"`
	Cwd           string   `json:"cwd"`
	PathMode      string   `json:"path_mode"`
	Query         string   `json:"query"`
	CaseSensitive bool     `json:"case_sensitive"`
	SearchKeys    bool     `json:"search_keys"`
	SearchValues  bool     `json:"search_values"`
	TypeFilter    []string `json:"type_filter"`
	PointerPrefix string   `json:"pointer_prefix"`
	Limit         int      `json:"limit"`
}

func (t *Tools) JSONOutline(raw json.RawMessage) (any, error) {
	var a JSONOutlineArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.MaxDepth <= 0 || a.MaxDepth > 10 {
		a.MaxDepth = 3
	}
	if a.MaxChildren <= 0 || a.MaxChildren > 500 {
		a.MaxChildren = 50
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	v, err := resolveJSONPointer(root, a.Pointer)
	if err != nil {
		return nil, err
	}
	node := outlineJSONValue(v, a.Pointer, 0, a.MaxDepth, a.MaxChildren)
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "pointer": a.Pointer, "root_type": jsonType(root), "outline": node, "bytes_read": size}, nil
}

func (t *Tools) JSONKeys(raw json.RawMessage) (any, error) {
	var a JSONKeysArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if a.Limit <= 0 || a.Limit > 1000 {
		a.Limit = 100
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	v, err := resolveJSONPointer(root, a.Pointer)
	if err != nil {
		return nil, err
	}
	switch vv := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(vv))
		for k := range vv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		end := minInt(len(keys), a.Offset+a.Limit)
		page := []string{}
		if a.Offset < len(keys) {
			page = keys[a.Offset:end]
		}
		return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "pointer": a.Pointer, "type": "object", "keys": page, "total": len(keys), "offset": a.Offset, "limit": a.Limit, "truncated": end < len(keys), "bytes_read": size}, nil
	case []any:
		total := len(vv)
		end := minInt(total, a.Offset+a.Limit)
		indexes := []int{}
		if a.Offset < total {
			for i := a.Offset; i < end; i++ {
				indexes = append(indexes, i)
			}
		}
		return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "pointer": a.Pointer, "type": "array", "indexes": indexes, "total": total, "offset": a.Offset, "limit": a.Limit, "truncated": end < total, "bytes_read": size}, nil
	default:
		return nil, fmt.Errorf("value at pointer %q has type %s and no children", a.Pointer, jsonType(v))
	}
}

func (t *Tools) JSONSlice(raw json.RawMessage) (any, error) {
	var a JSONSliceArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if a.Limit <= 0 || a.Limit > 500 {
		a.Limit = 50
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	v, err := resolveJSONPointer(root, a.Pointer)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value at pointer %q is %s, not array", a.Pointer, jsonType(v))
	}
	end := minInt(len(arr), a.Offset+a.Limit)
	items := []any{}
	if a.Offset < len(arr) {
		items = arr[a.Offset:end]
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "pointer": a.Pointer, "type": "array", "total": len(arr), "offset": a.Offset, "limit": a.Limit, "items": items, "truncated": end < len(arr), "bytes_read": size}, nil
}

func (t *Tools) JSONSearch(raw json.RawMessage) (any, error) {
	var a JSONSearchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, errors.New("query is required")
	}
	if !a.SearchKeys && !a.SearchValues {
		a.SearchKeys = true
		a.SearchValues = true
	}
	if a.Limit <= 0 || a.Limit > t.Cfg.Limits.MaxSearchResults {
		a.Limit = t.Cfg.Limits.MaxSearchResults
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	typeFilter := map[string]bool{}
	for _, typ := range a.TypeFilter {
		typeFilter[strings.TrimSpace(typ)] = true
	}
	q := a.Query
	if !a.CaseSensitive {
		q = strings.ToLower(q)
	}
	matches := []map[string]any{}
	var visit func(v any, pointer string)
	visit = func(v any, pointer string) {
		if len(matches) >= a.Limit {
			return
		}
		if a.PointerPrefix != "" && pointer != "" && !strings.HasPrefix(pointer, a.PointerPrefix) && !strings.HasPrefix(a.PointerPrefix, pointer) {
			return
		}
		switch vv := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(vv))
			for k := range vv {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				childPtr := joinJSONPointer(pointer, k)
				candidate := k
				if !a.CaseSensitive {
					candidate = strings.ToLower(candidate)
				}
				childType := jsonType(vv[k])
				if a.SearchKeys && strings.Contains(candidate, q) && jsonTypeAllowed(childType, typeFilter) {
					matches = append(matches, map[string]any{"pointer": childPtr, "match": "key", "type": childType, "preview": previewJSON(vv[k])})
				}
				if len(matches) >= a.Limit {
					return
				}
				visit(vv[k], childPtr)
				if len(matches) >= a.Limit {
					return
				}
			}
		case []any:
			for i, child := range vv {
				visit(child, joinJSONPointer(pointer, strconv.Itoa(i)))
				if len(matches) >= a.Limit {
					return
				}
			}
		default:
			if a.SearchValues {
				candidate := scalarString(v)
				if !a.CaseSensitive {
					candidate = strings.ToLower(candidate)
				}
				valueType := jsonType(v)
				if strings.Contains(candidate, q) && jsonTypeAllowed(valueType, typeFilter) {
					matches = append(matches, map[string]any{"pointer": pointer, "match": "value", "type": valueType, "preview": previewJSON(v)})
				}
			}
		}
	}
	visit(root, "")
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "query": a.Query, "pointer_prefix": a.PointerPrefix, "type_filter": a.TypeFilter, "matches": matches, "returned": len(matches), "limit": a.Limit, "truncated": len(matches) >= a.Limit, "bytes_read": size}, nil
}

func (t *Tools) JSONValidate(raw json.RawMessage) (any, error) {
	var a jsonPathArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "valid": true, "root_type": jsonType(root), "bytes_read": size}, nil
}

func resolveJSONPointer(root any, pointer string) (any, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON pointer must be empty or start with /: %q", pointer)
	}
	cur := root
	for _, rawPart := range strings.Split(pointer[1:], "/") {
		part, err := unescapeJSONPointerToken(rawPart)
		if err != nil {
			return nil, err
		}
		switch vv := cur.(type) {
		case map[string]any:
			next, ok := vv[part]
			if !ok {
				return nil, fmt.Errorf("pointer not found: %q", pointer)
			}
			cur = next
		case []any:
			if part == "" {
				return nil, fmt.Errorf("invalid array index in pointer %q", pointer)
			}
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(vv) {
				return nil, fmt.Errorf("array index out of range in pointer %q", pointer)
			}
			cur = vv[idx]
		default:
			return nil, fmt.Errorf("cannot traverse through %s at token %q", jsonType(cur), part)
		}
	}
	return cur, nil
}

func unescapeJSONPointerToken(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("invalid JSON pointer escape")
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", errors.New("invalid JSON pointer escape")
		}
		i++
	}
	return b.String(), nil
}

func escapeJSONPointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func joinJSONPointer(base, token string) string {
	if base == "" {
		return "/" + escapeJSONPointerToken(token)
	}
	return base + "/" + escapeJSONPointerToken(token)
}

func outlineJSONValue(v any, pointer string, depth, maxDepth, maxChildren int) map[string]any {
	node := map[string]any{"pointer": pointer, "type": jsonType(v)}
	switch vv := v.(type) {
	case map[string]any:
		node["children_count"] = len(vv)
		if depth >= maxDepth {
			node["truncated"] = len(vv) > 0
			return node
		}
		keys := make([]string, 0, len(vv))
		for k := range vv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		children := []map[string]any{}
		for i, k := range keys {
			if i >= maxChildren {
				node["truncated"] = true
				break
			}
			children = append(children, outlineJSONValue(vv[k], joinJSONPointer(pointer, k), depth+1, maxDepth, maxChildren))
		}
		node["children"] = children
	case []any:
		node["children_count"] = len(vv)
		if depth >= maxDepth {
			node["truncated"] = len(vv) > 0
			return node
		}
		children := []map[string]any{}
		for i, child := range vv {
			if i >= maxChildren {
				node["truncated"] = true
				break
			}
			children = append(children, outlineJSONValue(child, joinJSONPointer(pointer, strconv.Itoa(i)), depth+1, maxDepth, maxChildren))
		}
		node["children"] = children
	}
	return node
}

func jsonType(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func scalarString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func previewJSON(v any) string {
	s := scalarString(v)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func jsonTypeAllowed(typ string, filter map[string]bool) bool {
	return len(filter) == 0 || filter[typ]
}

func getJSONPathValue(obj map[string]any, path string) (any, bool) {
	if path == "" {
		return obj, true
	}
	var cur any = obj
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func jsonNumberAsFloat(v any, ok bool) (float64, bool) {
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f, err == nil
	}
}

func jsonEqualSimple(got, want any) bool { return fmt.Sprint(got) == fmt.Sprint(want) }
