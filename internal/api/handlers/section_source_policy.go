package handlers

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

func isTraktBackedSection(sectionType string, config json.RawMessage) bool {
	var values struct {
		Source         string `json:"source"`
		SourceProvider string `json:"source_provider"`
	}
	if len(config) == 0 || json.Unmarshal(config, &values) != nil {
		return false
	}
	switch sectionType {
	case "trending_discover":
		return values.Source == "trakt"
	case "collection":
		return values.SourceProvider == "trakt"
	default:
		return false
	}
}

func jsonConfigEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil {
		return reflect.DeepEqual(leftValue, rightValue)
	}
	return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
}

func hasSectionConfig(config json.RawMessage) bool {
	trimmed := bytes.TrimSpace(config)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func traktSectionKind(config json.RawMessage) string {
	if isTraktBackedSection("trending_discover", config) {
		return "trending_discover"
	}
	if isTraktBackedSection("collection", config) {
		return "collection"
	}
	return ""
}

func isTraktCollectionSourceConfig(config json.RawMessage) bool {
	var values struct {
		Provider string `json:"provider"`
		Mode     string `json:"mode"`
	}
	if len(config) == 0 || json.Unmarshal(config, &values) != nil {
		return false
	}
	return values.Provider == "trakt" || strings.HasPrefix(values.Mode, "trakt_")
}
