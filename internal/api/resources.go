package api

import (
	"encoding/json"
	"strings"
)

// Resource fields can also arrive nested in creator/admin JSON form values.
// Block inline URLs at the write boundary so a restart is not needed to clean
// newly submitted assets. Ordinary prose is not interpreted as a resource.
func hasInlineResourceParams(params map[string]string) bool {
	for key, value := range params {
		if inlineResource(key, value) {
			return true
		}
		if key == "season" || key == "videos" {
			var nested any
			if json.Unmarshal([]byte(value), &nested) == nil && inlineResourceTree(nested) {
				return true
			}
		}
	}
	return false
}
func inlineResource(key, value string) bool {
	switch key {
	case "face", "top_photo", "cover", "horizontal", "img", "pic", "avatar", "filename", "media", "source_url", "url":
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:")
	}
	return false
}
func inlineResourceTree(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if inlineResourceTree(item) {
				return true
			}
		}
	case map[string]any:
		for k, item := range x {
			if value, ok := item.(string); ok && inlineResource(k, value) {
				return true
			}
			if inlineResourceTree(item) {
				return true
			}
		}
	}
	return false
}
