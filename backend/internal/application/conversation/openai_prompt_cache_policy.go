package conversation

import (
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
)

const (
	openAIPromptCacheCapabilityKey = "promptCache"
	openAIPromptCacheOptionKey     = "prompt_cache_options"
	openAIPromptCacheRetentionKey  = "prompt_cache_retention"
)

type openAIPromptCacheCapabilityConfig struct {
	Enabled           bool
	EnabledConfigured bool
	Mode              string
	TTL               string
	Retention         string
}

// configureOpenAIPromptCacheForRoute 把路由能力收敛为上游请求所需的缓存键和选项。
// 官方 OpenAI 默认支持；兼容中转站必须在模型能力 JSON 中显式声明 promptCache.enabled=true。
func configureOpenAIPromptCacheForRoute(
	route *channel.ResolvedRoute,
	sessionID string,
	options map[string]interface{},
) (string, map[string]interface{}) {
	config, supported := resolveOpenAIPromptCacheRouteConfig(route)
	filtered := withoutOpenAIPromptCacheOptions(options)
	if supported {
		return strings.TrimSpace(sessionID), withOpenAIPromptCacheOptions(filtered, config)
	}
	return "", filtered
}

func supportsOpenAIPromptCacheRoute(route *channel.ResolvedRoute) bool {
	_, supported := resolveOpenAIPromptCacheRouteConfig(route)
	return supported
}

func resolveOpenAIPromptCacheRouteConfig(route *channel.ResolvedRoute) (openAIPromptCacheCapabilityConfig, bool) {
	if route == nil {
		return openAIPromptCacheCapabilityConfig{}, false
	}
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterOpenAIChatCompletions, llm.AdapterOpenAIResponses:
	default:
		return openAIPromptCacheCapabilityConfig{}, false
	}

	config := openAIPromptCacheCapability(route.ModelCapabilitiesJSON)
	if config.EnabledConfigured {
		return config, config.Enabled
	}
	return config, isOfficialOpenAIBaseURL(route.BaseURL)
}

func openAIPromptCacheCapability(capabilitiesJSON string) openAIPromptCacheCapabilityConfig {
	capabilities := decodeModelCapabilities(capabilitiesJSON)
	promptCache, ok := capabilities[openAIPromptCacheCapabilityKey].(map[string]interface{})
	if !ok {
		promptCache = nil
	}
	config := openAIPromptCacheCapabilityConfig{
		Mode:      strings.ToLower(strings.TrimSpace(modelOptionStringValue(promptCache["mode"]))),
		TTL:       strings.ToLower(strings.TrimSpace(modelOptionStringValue(promptCache["ttl"]))),
		Retention: normalizeOpenAIPromptCacheRetention(modelOptionStringValue(promptCache["retention"])),
	}
	config.Enabled, config.EnabledConfigured = promptCache["enabled"].(bool)
	if config.Mode != "explicit" && config.Retention == "" {
		config.Retention = legacyOpenAIPromptCacheRetention(capabilitiesJSON)
	}
	return config
}

func legacyOpenAIPromptCacheRetention(capabilitiesJSON string) string {
	defaults := modelCapabilityDefaultOptions(capabilitiesJSON)
	return normalizeOpenAIPromptCacheRetention(modelOptionStringValue(defaults[openAIPromptCacheRetentionKey]))
}

func withoutOpenAIPromptCacheOptions(options map[string]interface{}) map[string]interface{} {
	_, hasOptions := options[openAIPromptCacheOptionKey]
	_, hasRetention := options[openAIPromptCacheRetentionKey]
	if !hasOptions && !hasRetention {
		return options
	}
	filtered := cloneModelOptionMap(options)
	delete(filtered, openAIPromptCacheOptionKey)
	delete(filtered, openAIPromptCacheRetentionKey)
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func withOpenAIPromptCacheOptions(options map[string]interface{}, config openAIPromptCacheCapabilityConfig) map[string]interface{} {
	result := cloneModelOptionMap(options)
	switch config.Mode {
	case "explicit":
		cacheOptions := map[string]interface{}{"mode": "explicit"}
		if config.TTL == "30m" {
			cacheOptions["ttl"] = "30m"
		}
		if result == nil {
			result = make(map[string]interface{})
		}
		result[openAIPromptCacheOptionKey] = cacheOptions
	case "", "implicit":
		if config.Retention != "" {
			if result == nil {
				result = make(map[string]interface{})
			}
			result[openAIPromptCacheRetentionKey] = config.Retention
		}
	}
	return result
}

func usesExplicitOpenAIPromptCache(options map[string]interface{}) bool {
	raw, ok := options[openAIPromptCacheOptionKey].(map[string]interface{})
	if !ok {
		return false
	}
	mode, ok := raw["mode"].(string)
	return ok && strings.EqualFold(strings.TrimSpace(mode), "explicit")
}

func normalizeOpenAIPromptCacheRetention(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "in-memory", "in_memory":
		return "in_memory"
	case "24h":
		return "24h"
	default:
		return ""
	}
}
