package conversation

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
)

func TestConfigureOpenAIPromptCacheForRoute(t *testing.T) {
	tests := []struct {
		name          string
		route         *channel.ResolvedRoute
		wantKey       string
		wantMode      string
		wantTTL       string
		wantRetention string
	}{
		{
			name: "official OpenAI defaults to enabled",
			route: &channel.ResolvedRoute{
				Protocol: llm.AdapterOpenAIResponses,
				BaseURL:  "https://api.openai.com/v1",
			},
			wantKey: "session-1",
		},
		{
			name: "custom relay requires explicit capability",
			route: &channel.ResolvedRoute{
				Protocol: llm.AdapterOpenAIResponses,
				BaseURL:  "https://relay.example.com/v1",
			},
		},
		{
			name: "custom relay can opt in",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenAIChatCompletions,
				BaseURL:               "https://relay.example.com/v1",
				ModelCapabilitiesJSON: `{"promptCache":{"enabled":true}}`,
			},
			wantKey: "session-1",
		},
		{
			name: "custom relay can enable explicit caching",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenAIResponses,
				BaseURL:               "https://relay.example.com/v1",
				ModelCapabilitiesJSON: `{"promptCache":{"enabled":true,"mode":"explicit","ttl":"30m"}}`,
			},
			wantKey:  "session-1",
			wantMode: "explicit",
			wantTTL:  "30m",
		},
		{
			name: "official OpenAI supports implicit retention",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenAIChatCompletions,
				BaseURL:               "https://api.openai.com/v1",
				ModelCapabilitiesJSON: `{"promptCache":{"mode":"implicit","retention":"24h"}}`,
			},
			wantKey:       "session-1",
			wantRetention: "24h",
		},
		{
			name: "official OpenAI preserves legacy default retention",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenAIResponses,
				BaseURL:               "https://api.openai.com/v1",
				ModelCapabilitiesJSON: `{"defaultOptions":{"prompt_cache_retention":"24h"}}`,
			},
			wantKey:       "session-1",
			wantRetention: "24h",
		},
		{
			name: "official OpenAI can be disabled",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenAIResponses,
				BaseURL:               "https://api.openai.com/v1",
				ModelCapabilitiesJSON: `{"promptCache":{"enabled":false}}`,
			},
		},
		{
			name: "non OpenAI adapters stay disabled",
			route: &channel.ResolvedRoute{
				Protocol:              llm.AdapterOpenRouterResponses,
				BaseURL:               "https://openrouter.ai/api/v1",
				ModelCapabilitiesJSON: `{"promptCache":{"enabled":true}}`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := map[string]interface{}{
				"temperature": 0.2,
				"prompt_cache_options": map[string]interface{}{
					"mode": "user-controlled",
				},
				"prompt_cache_retention": "user-controlled",
			}
			key, options := configureOpenAIPromptCacheForRoute(test.route, " session-1 ", original)
			if key != test.wantKey {
				t.Fatalf("expected key %q, got %q", test.wantKey, key)
			}
			cacheOptions, _ := options["prompt_cache_options"].(map[string]interface{})
			if mode, _ := cacheOptions["mode"].(string); mode != test.wantMode {
				t.Fatalf("expected prompt cache mode %q, got %#v", test.wantMode, options)
			}
			if ttl, _ := cacheOptions["ttl"].(string); ttl != test.wantTTL {
				t.Fatalf("expected prompt cache ttl %q, got %#v", test.wantTTL, options)
			}
			if retention, _ := options["prompt_cache_retention"].(string); retention != test.wantRetention {
				t.Fatalf("expected prompt cache retention %q, got %#v", test.wantRetention, options)
			}
			if options["temperature"] != 0.2 {
				t.Fatalf("expected unrelated options to remain, got %#v", options)
			}
			if _, stillPresent := original["prompt_cache_options"]; !stillPresent {
				t.Fatalf("expected route filtering not to mutate caller options, got %#v", original)
			}
			if original["prompt_cache_retention"] != "user-controlled" {
				t.Fatalf("expected route filtering not to mutate caller retention, got %#v", original)
			}
		})
	}
}

func TestConfigureOpenAIPromptCacheForRouteDropsFieldsAfterFailoverToUnsupportedRoute(t *testing.T) {
	unsupportedRoute := &channel.ResolvedRoute{
		Protocol: llm.AdapterOpenAIResponses,
		BaseURL:  "https://legacy-relay.example.com/v1",
	}
	for _, capabilitiesJSON := range []string{
		`{"promptCache":{"enabled":true,"mode":"explicit","ttl":"30m"}}`,
		`{"promptCache":{"enabled":true,"mode":"implicit","retention":"24h"}}`,
	} {
		supportedRoute := &channel.ResolvedRoute{
			Protocol:              llm.AdapterOpenAIResponses,
			BaseURL:               "https://relay.example.com/v1",
			ModelCapabilitiesJSON: capabilitiesJSON,
		}
		key, options := configureOpenAIPromptCacheForRoute(supportedRoute, "session-1", map[string]interface{}{
			"temperature": 0.2,
		})
		if key != "session-1" {
			t.Fatalf("expected supported route cache key, got %q", key)
		}

		key, options = configureOpenAIPromptCacheForRoute(unsupportedRoute, "session-1", options)
		if key != "" {
			t.Fatalf("expected unsupported failover route to clear cache key, got %q", key)
		}
		if _, exists := options["prompt_cache_options"]; exists {
			t.Fatalf("expected unsupported failover route to drop prompt cache options, got %#v", options)
		}
		if _, exists := options["prompt_cache_retention"]; exists {
			t.Fatalf("expected unsupported failover route to drop prompt cache retention, got %#v", options)
		}
		if options["temperature"] != 0.2 {
			t.Fatalf("expected unrelated options to survive failover filtering, got %#v", options)
		}
	}
}

func TestConfigureOpenAIPromptCacheDoesNotDependOnModelOptionAllowlist(t *testing.T) {
	filtered := filterModelOptions(map[string]interface{}{
		"temperature":            0.2,
		"prompt_cache_options":   map[string]interface{}{"mode": "user-controlled"},
		"prompt_cache_retention": "user-controlled",
	}, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{
		Mode:             modelOptionPolicyAllowlist,
		AllowedPathsJSON: `{"default":["temperature"]}`,
	})
	route := &channel.ResolvedRoute{
		Protocol:              llm.AdapterOpenAIResponses,
		BaseURL:               "https://api.openai.com/v1",
		ModelCapabilitiesJSON: `{"promptCache":{"mode":"explicit","ttl":"30m"}}`,
	}

	key, options := configureOpenAIPromptCacheForRoute(route, "session-1", filtered)
	cacheOptions, _ := options["prompt_cache_options"].(map[string]interface{})
	if key != "session-1" || cacheOptions["mode"] != "explicit" || cacheOptions["ttl"] != "30m" {
		t.Fatalf("expected server cache policy to bypass the legacy user allowlist, key=%q options=%#v", key, options)
	}
}
