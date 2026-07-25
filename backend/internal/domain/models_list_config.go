package domain

// GroupModelsListConfig controls the optional custom model discovery list and
// the independent strict request allowlist for OpenAI groups.
type GroupModelsListConfig struct {
	Enabled bool `json:"enabled"`
	// Enforce turns the selected model list into an exact request allowlist for
	// OpenAI groups. It is intentionally independent from Enabled, which only
	// controls the /v1/models display response.
	Enforce bool     `json:"enforce"`
	Models  []string `json:"models,omitempty"`
}
