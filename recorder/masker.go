package recorder

import "strings"

var sensitiveKeys = []string{
	"x-api-key",
	"authorization",
	"x-auth-token",
	"cookie",
}

func MaskHeaders(headers map[string]string) map[string]string {
	masked := make(map[string]string, len(headers))
	for k, v := range headers {
		if isSensitive(k) {
			masked[k] = "***MASKED***"
		} else {
			masked[k] = v
		}
	}
	return masked
}

func isSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if lower == s {
			return true
		}
	}
	return false
}
