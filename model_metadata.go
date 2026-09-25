package main

import (
	"encoding/json"
	"strings"
	"sync"
)

var (
	gpt56MetadataOnce        sync.Once
	gpt56Metadata            map[string]map[string]any
	bundledModelMetadataOnce sync.Once
	bundledModelMetadata     map[string]map[string]any
)

func loadGPT56Metadata() map[string]map[string]any {
	gpt56MetadataOnce.Do(func() {
		gpt56Metadata = map[string]map[string]any{}
		var payload struct {
			Models []map[string]any `json:"models"`
		}
		if json.Unmarshal(gpt56ModelMetadataJSON, &payload) != nil {
			return
		}
		for _, model := range payload.Models {
			slug := strings.TrimSpace(stringFromAny(model["slug"]))
			if slug != "" {
				gpt56Metadata[slug] = model
			}
		}
	})
	return gpt56Metadata
}

func isGPT56Model(slug string) bool {
	_, ok := loadGPT56Metadata()[strings.TrimSpace(slug)]
	return ok
}

func gpt56CatalogEntry(slug string) map[string]any {
	source := loadGPT56Metadata()[strings.TrimSpace(slug)]
	return cloneModelMetadata(source)
}

func bundledModelCatalogEntry(slug string) map[string]any {
	bundledModelMetadataOnce.Do(func() {
		bundledModelMetadata = map[string]map[string]any{}
		for _, payload := range [][]byte{gpt56ModelMetadataJSON, astraModelMetadataJSON} {
			var decoded struct {
				Models []map[string]any `json:"models"`
			}
			if json.Unmarshal(payload, &decoded) != nil {
				continue
			}
			for _, model := range decoded.Models {
				slug := strings.TrimSpace(stringFromAny(model["slug"]))
				if slug != "" {
					bundledModelMetadata[slug] = model
				}
			}
		}
	})
	return cloneModelMetadata(bundledModelMetadata[strings.TrimSpace(slug)])
}

func cloneModelMetadata(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	data, _ := json.Marshal(source)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func modelMetadataForNames(names []string) map[string]any {
	result := map[string]any{}
	for _, name := range names {
		metadata := bundledModelCatalogEntry(name)
		if metadata == nil {
			continue
		}
		levels, _ := metadata["supported_reasoning_levels"].([]any)
		reasoning := make([]any, 0, len(levels))
		for _, raw := range levels {
			level, _ := raw.(map[string]any)
			effort := strings.TrimSpace(stringFromAny(level["effort"]))
			if effort == "" {
				continue
			}
			reasoning = append(reasoning, map[string]any{
				"reasoningEffort": effort,
				"description":     stringFromAny(level["description"]),
			})
		}
		result[name] = map[string]any{
			"displayName":               firstNonEmpty(stringFromAny(metadata["display_name"]), name),
			"description":               firstNonEmpty(stringFromAny(metadata["description"]), "Custom model"),
			"defaultReasoningEffort":    firstNonEmpty(stringFromAny(metadata["default_reasoning_level"]), "medium"),
			"supportedReasoningEfforts": reasoning,
			"additionalSpeedTiers":      firstNonNil(metadata["additional_speed_tiers"], []any{}),
			"serviceTiers":              firstNonNil(metadata["service_tiers"], []any{}),
		}
	}
	return result
}
