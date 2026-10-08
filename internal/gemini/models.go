package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ModelOption struct {
	ID                    string
	DisplayName           string
	SupportedEfforts []string
	DefaultEffort    string
}

func (m ModelOption) Label() string {
	if m.DisplayName == "" || strings.EqualFold(m.DisplayName, m.ID) {
		return m.ID
	}
	return m.DisplayName
}

func (m ModelOption) Description() string {
	if m.DefaultEffort != "" && len(m.SupportedEfforts) > 0 {
		return fmt.Sprintf("%s · default %s · %s", m.Label(), m.DefaultEffort, strings.Join(m.SupportedEfforts, "/"))
	}
	if len(m.SupportedEfforts) > 0 {
		return fmt.Sprintf("%s · %s", m.Label(), strings.Join(m.SupportedEfforts, "/"))
	}
	return m.Label()
}

func decodeModelCatalog(raw []byte) ([]ModelOption, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode model catalog: %w", err)
	}

	var out []ModelOption
	seen := map[string]bool{}
	for _, key := range []string{"models", "modelInfos", "availableModels", "availableModelInfos"} {
		payload, ok := envelope[key]
		if !ok {
			continue
		}
		items, err := decodeModelItems(payload)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", key, err)
		}
		for _, item := range items {
			item.ID = normalizeDiscoveredModelID(item.ID)
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			item.SupportedEfforts = uniqueLower(item.SupportedEfforts)
			if item.DefaultEffort != "" {
				item.DefaultEffort = strings.ToLower(item.DefaultEffort)
			}
			sort.SliceStable(item.SupportedEfforts, func(i, j int) bool {
				rank := func(level string) int {
					switch level {
					case "low":
						return 0
					case "medium":
						return 1
					case "high":
						return 2
					default:
						return 3
					}
				}
				ri, rj := rank(item.SupportedEfforts[i]), rank(item.SupportedEfforts[j])
				if ri == rj {
					return item.SupportedEfforts[i] < item.SupportedEfforts[j]
				}
				return ri < rj
			})
			out = append(out, item)
		}
		if len(out) > 0 {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("Antigravity returned no selectable models")
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].Label()), strings.ToLower(out[j].Label())
		if li == lj {
			return out[i].ID < out[j].ID
		}
		return li < lj
	})
	return out, nil
}

func decodeModelItems(payload json.RawMessage) ([]ModelOption, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(payload, &arr); err == nil {
		out := make([]ModelOption, 0, len(arr))
		for _, item := range arr {
			var s string
			if json.Unmarshal(item, &s) == nil {
				out = append(out, ModelOption{ID: s})
				continue
			}
			var obj map[string]any
			if json.Unmarshal(item, &obj) != nil || !selectableModel(obj) {
				continue
			}
			out = append(out, modelOptionFromMap(obj))
		}
		return out, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, err
	}
	out := make([]ModelOption, 0, len(obj))
	for key, item := range obj {
		var nested map[string]any
		if json.Unmarshal(item, &nested) == nil {
			if !selectableModel(nested) {
				continue
			}
			m := modelOptionFromMap(nested)
			if m.ID == "" {
				m.ID = key
			}
			out = append(out, m)
			continue
		}
		out = append(out, ModelOption{ID: key})
	}
	return out, nil
}

func selectableModel(obj map[string]any) bool {
	if hidden, ok := obj["hidden"].(bool); ok && hidden {
		return false
	}

	for _, key := range []string{"supportedGenerationMethods", "supportedMethods"} {
		value, ok := obj[key]
		if !ok {
			continue
		}
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			continue
		}
		for _, item := range items {
			if method, ok := item.(string); ok {
				method = strings.ToLower(method)
				if method == "generatecontent" || method == "streamgeneratecontent" {
					return true
				}
			}
		}
		return false
	}

	return true
}

func modelOptionFromMap(obj map[string]any) ModelOption {
	m := ModelOption{}
	for _, key := range []string{"model", "modelId", "id", "name", "slug"} {
		value, ok := obj[key].(string)
		if !ok {
			continue
		}
		if id := normalizeDiscoveredModelID(value); id != "" {
			m.ID = id
			break
		}
	}
	for _, key := range []string{"displayName", "display_name", "label", "title"} {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			m.DisplayName = value
			break
		}
	}
	m.SupportedEfforts = extractReasoningEfforts(obj)
	for _, key := range []string{"defaultReasoningEffort", "defaultReasoningLevel", "defaultThinkingLevel"} {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			m.DefaultEffort = value
			break
		}
	}
	return m
}

func extractReasoningEfforts(obj map[string]any) []string {
	var out []string
	for _, key := range []string{
		"supportedReasoningEfforts",
		"supportedReasoningLevels",
		"supportedThinkingLevels",
		"reasoningEfforts",
	} {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch items := value.(type) {
		case []any:
			for _, item := range items {
				switch v := item.(type) {
				case string:
					out = append(out, v)
				case map[string]any:
					for _, name := range []string{"reasoningEffort", "effort", "level", "value"} {
						if s, ok := v[name].(string); ok {
							out = append(out, s)
							break
						}
					}
				}
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

func normalizeDiscoveredModelID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "models/")
	if strings.ContainsAny(id, " \t\r\n") {
		return ""
	}
	if id == "" {
		return ""
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '/') {
			return ""
		}
	}
	return id
}

func uniqueLower(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
