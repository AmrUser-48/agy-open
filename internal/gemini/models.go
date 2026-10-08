package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ModelOption struct {
	ID               string
	DisplayName      string
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
	seenID := map[string]bool{}
	seenLabel := map[string]bool{}
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
			if item.ID == "" {
				continue
			}
			if canonical := canonicalDiscoveredModelID(item.DisplayName); canonical != "" {
				item.ID = canonical
			}
			if !isSupportedSelectableModel(item.ID) {
				continue
			}
			labelKey := strings.ToLower(strings.Join(strings.Fields(item.Label()), " "))
			if seenID[item.ID] || (labelKey != "" && seenLabel[labelKey]) {
				continue
			}
			seenID[item.ID] = true
			if labelKey != "" {
				seenLabel[labelKey] = true
			}
			item.SupportedEfforts = uniqueLower(item.SupportedEfforts)
			if item.DefaultEffort != "" {
				item.DefaultEffort = strings.ToLower(item.DefaultEffort)
			}
			sort.SliceStable(item.SupportedEfforts, func(i, j int) bool {
				return effortRank(item.SupportedEfforts[i]) < effortRank(item.SupportedEfforts[j])
			})
			out = append(out, item)
		}
		if len(out) > 0 {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("Antigravity returned no selectable current models")
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := modelRank(out[i].ID), modelRank(out[j].ID)
		if ri != rj {
			return ri < rj
		}
		li, lj := strings.ToLower(out[i].Label()), strings.ToLower(out[j].Label())
		return li < lj
	})
	return out, nil
}

func effortRank(level string) int {
	switch strings.ToLower(level) {
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

func modelRank(id string) int {
	order := []string{
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-low",
		"gemini-3.7-flash-high",
		"gemini-3.7-flash-medium",
		"gemini-3.7-flash-low",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-low",
		"gemini-3.1-pro-high",
		"gemini-3.1-pro-low",
		"claude-opus-5-5-low",
		"claude-opus-5-5-medium",
		"claude-opus-5-5-high",
		"claude-sonnet-5-5-low",
		"claude-sonnet-5-5-medium",
		"claude-sonnet-5-5-high",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b-medium",
	}
	for i, candidate := range order {
		if id == candidate {
			return i
		}
	}
	return len(order) + 1
}

func isSupportedSelectableModel(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-low",
		"gemini-3.7-flash-high",
		"gemini-3.7-flash-medium",
		"gemini-3.7-flash-low",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-low",
		"gemini-3.1-pro-high",
		"gemini-3.1-pro-low",
		"claude-opus-5-5-low",
		"claude-opus-5-5-medium",
		"claude-opus-5-5-high",
		"claude-sonnet-5-5-low",
		"claude-sonnet-5-5-medium",
		"claude-sonnet-5-5-high",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b-medium":
		return true
	default:
		return false
	}
}

func decodeModelItems(payload json.RawMessage) ([]ModelOption, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(payload, &arr); err == nil {
		out := make([]ModelOption, 0, len(arr))
		for _, item := range arr {
			var s string
			if json.Unmarshal(item, &s) == nil {
				out = append(out, ModelOption{ID: canonicalDiscoveredModelID(s)})
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
		canonicalKey := canonicalDiscoveredModelID(key)
		var nested map[string]any
		if json.Unmarshal(item, &nested) == nil {
			if !selectableModel(nested) {
				continue
			}
			m := modelOptionFromMap(nested)
			if canonicalKey != "" {
				m.ID = canonicalKey
			}
			if m.ID != "" {
				out = append(out, m)
			}
			continue
		}
		if canonicalKey != "" {
			out = append(out, ModelOption{ID: canonicalKey})
		}
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
	for _, key := range []string{"displayName", "display_name", "label", "title", "name"} {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			m.DisplayName = value
			break
		}
	}
	for _, key := range []string{"catalogId", "catalogID", "modelId", "modelID", "id", "slug", "model", "name"} {
		value, ok := obj[key].(string)
		if !ok {
			continue
		}
		if id := canonicalDiscoveredModelID(value); id != "" {
			m.ID = id
			break
		}
	}
	if m.ID == "" {
		m.ID = canonicalDiscoveredModelID(m.DisplayName)
	}
	if canonical := canonicalDiscoveredModelID(m.DisplayName); canonical != "" {
		m.ID = canonical
	}
	m.SupportedEfforts = extractReasoningEfforts(obj)
	for _, key := range []string{"defaultReasoningEffort", "defaultReasoningLevel", "defaultThinkingLevel"} {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			m.DefaultEffort = value
			break
		}
	}
	if inferred := inferModelEffort(m.ID, m.DisplayName); inferred != "" {
		if len(m.SupportedEfforts) == 0 {
			m.SupportedEfforts = []string{inferred}
		}
		if m.DefaultEffort == "" {
			m.DefaultEffort = inferred
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
	if strings.ContainsAny(id, " \t\r\n") || id == "" {
		return ""
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '/') {
			return ""
		}
	}
	return id
}

func canonicalDiscoveredModelID(value string) string {
	id := normalizeDiscoveredModelID(value)
	lower := strings.ToLower(id)
	if id != "" && !isInternalModelID(lower) {
		if !isSupportedSelectableModel(lower) {
			return ""
		}
		switch lower {
		case "gemini-3.8-flash", "gemini-3.8-flash-tiered":
			return "gemini-3.8-flash-medium"
		case "gemini-3.7-flash", "gemini-3.7-flash-tiered":
			return "gemini-3.7-flash-medium"
		case "gemini-3.6-flash", "gemini-3.6-flash-tiered":
			return "gemini-3.6-flash-medium"
		case "gemini-3.1-pro":
			return "gemini-3.1-pro-high"
		case "claude-opus-5-5":
			return "claude-opus-5-5-medium"
		case "claude-sonnet-5-5":
			return "claude-sonnet-5-5-medium"
		case "claude-opus-4-6":
			return "claude-opus-4-6-thinking"
		case "gpt-oss-120b":
			return "gpt-oss-120b-medium"
		default:
			return id
		}
	}
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, "–", "-")
	name = strings.ReplaceAll(name, "—", "-")
	name = strings.Join(strings.Fields(name), " ")
	switch name {
	case "gemini 3.8 flash (high)":
		return "gemini-3.8-flash-high"
	case "gemini 3.8 flash (medium)", "gemini 3.8 flash":
		return "gemini-3.8-flash-medium"
	case "gemini 3.8 flash (low)":
		return "gemini-3.8-flash-low"
	case "gemini 3.7 flash (high)":
		return "gemini-3.7-flash-high"
	case "gemini 3.7 flash (medium)", "gemini 3.7 flash":
		return "gemini-3.7-flash-medium"
	case "gemini 3.7 flash (low)":
		return "gemini-3.7-flash-low"
	case "gemini 3.6 flash (high)":
		return "gemini-3.6-flash-high"
	case "gemini 3.6 flash (medium)", "gemini 3.6 flash":
		return "gemini-3.6-flash-medium"
	case "gemini 3.6 flash (low)":
		return "gemini-3.6-flash-low"
	case "gemini 3.1 pro (high)", "gemini 3.1 pro":
		return "gemini-3.1-pro-high"
	case "gemini 3.1 pro (low)":
		return "gemini-3.1-pro-low"
	case "claude opus 5.5 (low)", "claude opus 5.5 (thinking, low)":
		return "claude-opus-5-5-low"
	case "claude opus 5.5 (medium)", "claude opus 5.5 (thinking, medium)", "claude opus 5.5 (thinking)":
		return "claude-opus-5-5-medium"
	case "claude opus 5.5 (high)", "claude opus 5.5 (thinking, high)":
		return "claude-opus-5-5-high"
	case "claude sonnet 5.5 (low)", "claude sonnet 5.5 (thinking, low)":
		return "claude-sonnet-5-5-low"
	case "claude sonnet 5.5 (medium)", "claude sonnet 5.5 (thinking, medium)", "claude sonnet 5.5 (thinking)":
		return "claude-sonnet-5-5-medium"
	case "claude sonnet 5.5 (high)", "claude sonnet 5.5 (thinking, high)":
		return "claude-sonnet-5-5-high"
	case "claude sonnet 4.6 (thinking)", "claude sonnet 4.6":
		return "claude-sonnet-4-6"
	case "claude opus 4.6 (thinking)", "claude opus 4.6":
		return "claude-opus-4-6-thinking"
	case "gpt-oss 120b (medium)", "gpt-oss 120b", "model_openai_gpt_oss_120b_medium":
		return "gpt-oss-120b-medium"
	default:
		return ""
	}
}

func isInternalModelID(id string) bool {
	return strings.HasPrefix(id, "model_") ||
		strings.HasPrefix(id, "chat_") ||
		strings.HasPrefix(id, "tab_")
}

func inferModelEffort(id, display string) string {
	id = strings.ToLower(id)
	for _, level := range []string{"low", "medium", "high"} {
		if strings.HasSuffix(id, "-"+level) || strings.Contains(strings.ToLower(display), "("+level+")") {
			return level
		}
	}
	return ""
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
