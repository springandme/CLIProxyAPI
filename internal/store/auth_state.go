package store

import (
	"fmt"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func authDisabledState(metadata map[string]any) (bool, cliproxyauth.Status) {
	disabled := false
	if metadata != nil {
		if rawDisabled, ok := metadata["disabled"]; ok {
			disabled = parseDisabledValue(rawDisabled)
		}
	}
	status := cliproxyauth.StatusActive
	if disabled {
		status = cliproxyauth.StatusDisabled
	}
	return disabled, status
}

func persistDisabledMetadata(auth *cliproxyauth.Auth) {
	if auth == nil || auth.Metadata == nil {
		return
	}
	auth.Metadata["disabled"] = auth.Disabled
}

func parseDisabledValue(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	case float64:
		return v != 0
	case float32:
		return v != 0
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw)), "true")
	}
}
