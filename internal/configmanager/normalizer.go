package configmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const autoProviderName = "auto"

func (m *Manager) NormalizeActiveConfig() error {
	normalized, changed, err := normalizeConfigFile(m.cfg.OpenClawConfigPath)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return os.WriteFile(m.cfg.OpenClawConfigPath, normalized, 0o600)
}

func normalizeConfigFile(path string) ([]byte, bool, error) {
	overrides, hasOverrides, err := readLLMOverridesFromEnv()
	if err != nil || !hasOverrides {
		return nil, false, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read openclaw config: %w", err)
	}

	normalized, err := normalizeLLMConfigContent(content, overrides)
	if err != nil {
		return nil, false, err
	}
	if bytes.Equal(content, normalized) {
		return normalized, false, nil
	}
	return normalized, true, nil
}

func normalizeConfigContent(content []byte) ([]byte, error) {
	overrides, hasOverrides, err := readLLMOverridesFromEnv()
	if err != nil {
		return nil, err
	}
	if !hasOverrides {
		return content, nil
	}
	return normalizeLLMConfigContent(content, overrides)
}

type llmOverrides struct {
	BaseURL   string
	APIKey    string
	APIKeySet bool
	ModelIDs  []string
}

func readLLMOverridesFromEnv() (llmOverrides, bool, error) {
	var overrides llmOverrides

	if raw := strings.TrimSpace(os.Getenv("CLAWMANAGER_LLM_MODEL")); raw != "" {
		modelIDs, err := parseModelIDs(raw)
		if err != nil {
			return llmOverrides{}, false, err
		}
		overrides.ModelIDs = modelIDs
	}

	overrides.BaseURL = firstNonEmptyEnv("CLAWMANAGER_LLM_BASE_URL", "OPENAI_BASE_URL")
	overrides.APIKey, overrides.APIKeySet = firstLookupEnv("CLAWMANAGER_LLM_API_KEY", "OPENAI_API_KEY")

	hasOverrides := overrides.BaseURL != "" || overrides.APIKeySet || len(overrides.ModelIDs) > 0
	return overrides, hasOverrides, nil
}

func parseModelIDs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if strings.HasPrefix(raw, "[") {
		var parsed []any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, fmt.Errorf("parse CLAWMANAGER_LLM_MODEL array: %w", err)
		}
		modelIDs := uniqueNonEmptyModelIDs(parsed)
		if len(modelIDs) == 0 {
			return nil, fmt.Errorf("parse CLAWMANAGER_LLM_MODEL array: no model ids found")
		}
		return modelIDs, nil
	}

	return []string{raw}, nil
}

func uniqueNonEmptyModelIDs(values []any) []string {
	seen := make(map[string]struct{}, len(values))
	modelIDs := make([]string, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(fmt.Sprint(value))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		modelIDs = append(modelIDs, id)
	}
	return modelIDs
}

func normalizeLLMConfigContent(content []byte, overrides llmOverrides) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("parse openclaw config: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}

	models := ensureObject(cfg, "models")
	providers := ensureObject(models, "providers")
	autoProvider := ensureObject(providers, autoProviderName)

	if overrides.BaseURL != "" {
		autoProvider["baseUrl"] = overrides.BaseURL
	}
	if overrides.APIKeySet {
		autoProvider["apiKey"] = overrides.APIKey
	}
	if len(overrides.ModelIDs) > 0 {
		autoProvider["models"] = buildProviderModels(autoProvider["models"], overrides.ModelIDs)

		agents := ensureObject(cfg, "agents")
		defaults := ensureObject(agents, "defaults")
		model := ensureObject(defaults, "model")
		model["primary"] = qualifiedModelID(overrides.ModelIDs[0])
		defaults["models"] = buildAgentModels(defaults["models"], overrides.ModelIDs)
	}

	normalized, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("marshal openclaw config: %w", err)
	}
	normalized = append(normalized, '\n')
	return normalized, nil
}

func buildProviderModels(existing any, modelIDs []string) []any {
	byID := indexModelsByID(existing)
	models := make([]any, 0, len(modelIDs))
	for _, id := range modelIDs {
		if current, ok := byID[id]; ok {
			cloned := cloneMap(current)
			cloned["id"] = id
			if strings.EqualFold(id, "auto") || strings.TrimSpace(stringValue(cloned["name"])) == "" {
				cloned["name"] = displayModelName(id)
			}
			models = append(models, cloned)
			continue
		}
		models = append(models, defaultProviderModel(id))
	}
	return models
}

func indexModelsByID(existing any) map[string]map[string]any {
	items, ok := existing.([]any)
	if !ok {
		return map[string]map[string]any{}
	}

	index := make(map[string]map[string]any, len(items))
	for _, item := range items {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(stringValue(model["id"]))
		if id == "" {
			continue
		}
		index[id] = model
	}
	return index
}

func buildAgentModels(existing any, modelIDs []string) map[string]any {
	current, _ := existing.(map[string]any)
	models := make(map[string]any, len(modelIDs))
	for _, id := range modelIDs {
		key := qualifiedModelID(id)
		if current != nil {
			if value, ok := current[key]; ok {
				models[key] = value
				continue
			}
		}
		models[key] = map[string]any{}
	}
	return models
}

func defaultProviderModel(id string) map[string]any {
	return map[string]any{
		"id":        id,
		"name":      displayModelName(id),
		"reasoning": false,
		"input": []any{
			"text",
		},
		"cost": map[string]any{
			"input":      0,
			"output":     0,
			"cacheRead":  0,
			"cacheWrite": 0,
		},
		"contextWindow": 64000,
		"maxTokens":     8192,
	}
}

func qualifiedModelID(id string) string {
	return autoProviderName + "/" + id
}

func displayModelName(id string) string {
	if strings.EqualFold(id, "auto") {
		return "Auto"
	}
	return id
}

func ensureObject(parent map[string]any, key string) map[string]any {
	if current, ok := parent[key].(map[string]any); ok {
		return current
	}
	current := map[string]any{}
	parent[key] = current
	return current
}

func cloneMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func stringValue(value any) string {
	switch raw := value.(type) {
	case string:
		return raw
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func firstLookupEnv(keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			return value, true
		}
	}
	return "", false
}
