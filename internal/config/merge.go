package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Markers recognised in policy overrides. They are removed before the merged
// policies are decoded.
const (
	// RemoveMarker on an item of a keyed list drops the matching top-level
	// item, e.g. {name: develop, $remove: true}.
	RemoveMarker = "$remove"
	// ReplaceMarker wraps a list that replaces the top-level list as a whole
	// instead of being merged with it, e.g. protected_branches: {$replace: [...]}.
	ReplaceMarker = "$replace"
)

// keyedLists maps the path of each policy list whose items have a natural
// identity to the fields that form it. Override items are matched to
// top-level items by these fields; lists not listed here are replaced.
var keyedLists = map[string][]string{
	"protected_branches":           {"name"},
	"protected_tags.rules":         {"name"},
	"protected_environments.rules": {"name"},
	"approval_rules.rules":         {"name"},
	"target_branch_rules.rules":    {"name"},
	"variables":                    {"key", "environment_scope"},
	"webhooks":                     {"url"},
	"repository_files":             {"path"},
	"branch_merges":                {"source_branch", "target_branch"},
	"runners.runners":              {"id"},
	"members.allowed_members":      {"username"},
}

// listOrRules are sections that accept either a list of rules or a mapping
// with prune and rules; a list is treated as {rules: [...]}.
var listOrRules = map[string]bool{
	"protected_tags":         true,
	"protected_environments": true,
}

// keyDefaults gives the value a missing key field takes, so that items which
// omit it still match (a variable without environment_scope applies to "*").
var keyDefaults = map[string]string{"environment_scope": "*"}

// MergePolicies returns base with override merged over it:
//
//   - Mappings merge field by field.
//   - Lists with a natural key (see keyedLists) merge item by item: an
//     override item whose key matches a top-level item is merged into it, a
//     new key is appended, and an item with "$remove: true" drops the
//     matching top-level item. Wrapping a list as {$replace: [...]} replaces
//     the top-level list instead.
//   - Other lists and scalars replace the top-level value.
//   - null removes the section or field.
//
// The result is decoded strictly, so unknown fields are rejected. base is
// not modified.
func MergePolicies(base PoliciesConfig, override map[string]any) (*PoliciesConfig, error) {
	raw, err := yaml.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("encode policies: %w", err)
	}
	merged := map[string]any{}
	if err := yaml.Unmarshal(raw, &merged); err != nil {
		return nil, fmt.Errorf("decode policies: %w", err)
	}
	if merged == nil {
		merged = map[string]any{}
	}

	if err := mergeMap(merged, override, ""); err != nil {
		return nil, err
	}
	if err := rejectMarkers(merged, ""); err != nil {
		return nil, err
	}

	out := &PoliciesConfig{}
	if len(merged) == 0 {
		return out, nil
	}
	raw, err = yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged policies: %w", err)
	}
	if err := UnmarshalStrict(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// mergeMap merges src into dst in place.
func mergeMap(dst, src map[string]any, path string) error {
	for _, k := range sortedKeys(src) {
		v := src[k]
		p := joinPath(path, k)
		if v == nil {
			delete(dst, k)
			continue
		}
		mv, err := mergeValue(dst[k], v, p)
		if err != nil {
			return err
		}
		dst[k] = mv
	}
	return nil
}

func mergeValue(dst, src any, path string) (any, error) {
	if list, ok := src.([]any); ok && listOrRules[path] {
		src = map[string]any{"rules": list}
	}
	if list, ok := dst.([]any); ok && listOrRules[path] {
		dst = map[string]any{"rules": list}
	}

	if srcMap, ok := asStringMap(src); ok {
		if repl, has := srcMap[ReplaceMarker]; has {
			if len(srcMap) != 1 {
				return nil, fmt.Errorf("%s: %s must be the only field", path, ReplaceMarker)
			}
			list, ok := repl.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: %s takes a list", path, ReplaceMarker)
			}
			return list, nil
		}
		dstMap, ok := asStringMap(dst)
		if !ok {
			dstMap = map[string]any{}
		}
		if err := mergeMap(dstMap, srcMap, path); err != nil {
			return nil, err
		}
		return dstMap, nil
	}

	if srcList, ok := src.([]any); ok {
		if keys, keyed := keyedLists[path]; keyed {
			dstList, _ := dst.([]any)
			return mergeKeyedList(dstList, srcList, keys, path)
		}
	}
	return src, nil
}

func mergeKeyedList(dst, src []any, keys []string, path string) ([]any, error) {
	out := make([]any, 0, len(dst)+len(src))
	index := make(map[string]int, len(dst))
	for _, item := range dst {
		m, ok := asStringMap(item)
		if !ok {
			out = append(out, item)
			continue
		}
		index[itemKey(m, keys)] = len(out)
		out = append(out, m)
	}

	removed := map[int]bool{}
	seen := map[string]bool{}
	for i, item := range src {
		m, ok := asStringMap(item)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected a mapping", path, i)
		}
		for _, k := range keys {
			if _, has := m[k]; !has && keyDefaults[k] == "" {
				return nil, fmt.Errorf("%s[%d]: %s is required to match the item against the top-level list", path, i, k)
			}
		}
		key := itemKey(m, keys)
		if seen[key] {
			return nil, fmt.Errorf("%s[%d]: duplicate item %s", path, i, describeKey(m, keys))
		}
		seen[key] = true

		if rm, has := m[RemoveMarker]; has {
			if rm != true {
				return nil, fmt.Errorf("%s[%d]: %s must be true", path, i, RemoveMarker)
			}
			pos, exists := index[key]
			if !exists {
				return nil, fmt.Errorf("%s[%d]: cannot remove %s: it is not in the top-level list", path, i, describeKey(m, keys))
			}
			removed[pos] = true
			continue
		}

		itemPath := fmt.Sprintf("%s[%s]", path, describeKey(m, keys))
		if pos, exists := index[key]; exists {
			existing, _ := asStringMap(out[pos])
			if err := mergeMap(existing, m, itemPath); err != nil {
				return nil, err
			}
			out[pos] = existing
			continue
		}
		fresh := map[string]any{}
		if err := mergeMap(fresh, m, itemPath); err != nil {
			return nil, err
		}
		index[key] = len(out)
		out = append(out, fresh)
	}

	if len(removed) == 0 {
		return out, nil
	}
	kept := make([]any, 0, len(out)-len(removed))
	for i, item := range out {
		if !removed[i] {
			kept = append(kept, item)
		}
	}
	return kept, nil
}

func itemKey(m map[string]any, keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		v, ok := m[k]
		if !ok || v == nil || fmt.Sprint(v) == "" {
			parts[i] = keyDefaults[k]
			continue
		}
		parts[i] = fmt.Sprint(normaliseNumber(v))
	}
	return strings.Join(parts, "\x00")
}

func describeKey(m map[string]any, keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			v = keyDefaults[k]
		}
		parts[i] = fmt.Sprintf("%s=%v", k, normaliseNumber(v))
	}
	return strings.Join(parts, ",")
}

// normaliseNumber makes JSON's float64 and YAML's int compare equal for
// whole numbers such as runner IDs.
func normaliseNumber(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}

// rejectMarkers reports a marker that was used somewhere it has no meaning.
func rejectMarkers(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(t) {
			if strings.HasPrefix(k, "$") {
				return fmt.Errorf("%s: %s is only allowed on items of a list matched by key (for example protected_branches or variables)", joinPath(path, k), k)
			}
			if err := rejectMarkers(t[k], joinPath(path, k)); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range t {
			if err := rejectMarkers(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asStringMap normalises the map types produced by the YAML and JSON
// decoders.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out, true
	default:
		return nil, false
	}
}
