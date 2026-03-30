package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Extraction — pull values from an HTTP response and store in variables
// ─────────────────────────────────────────────────────────────────────────────

// ResponseData bundles all extractable fields from a completed HTTP request.
type ResponseData struct {
	// Status is the numeric HTTP status code.
	Status int
	// Headers are the response headers (keys already lowercased).
	Headers map[string]string
	// Body is the raw response body bytes.
	Body []byte
}

// ApplyExtractions evaluates each extraction rule in rules and stores the
// result in vars.
//
// Path syntax (case-insensitive prefix match):
//
//	response.status              → the HTTP status code as a string ("200")
//	response.headers.<name>      → value of a response header
//	response.body.<key>          → top-level field from a JSON body
//	response.body.<a>.<b>.<c>    → nested JSON field (dot-separated)
//
// If a path cannot be resolved the variable is set to "" and a descriptive
// error is returned (non-fatal — other rules continue to be evaluated).
// All errors are collected and returned as a single combined error.
func ApplyExtractions(rules map[string]string, resp *ResponseData, vars map[string]string) error {
	var errs []string
	for varName, path := range rules {
		val, err := resolvePath(path, resp)
		if err != nil {
			errs = append(errs, fmt.Sprintf("extract %q from %q: %v", varName, path, err))
			vars[varName] = ""
			continue
		}
		vars[varName] = val
	}
	if len(errs) > 0 {
		return fmt.Errorf("extraction errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// resolvePath evaluates a single extraction path against the response.
func resolvePath(path string, resp *ResponseData) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(path))

	switch {
	case lower == "response.status":
		return strconv.Itoa(resp.Status), nil

	case strings.HasPrefix(lower, "response.headers."):
		name := lower[len("response.headers."):]
		if val, ok := resp.Headers[name]; ok {
			return val, nil
		}
		return "", fmt.Errorf("header %q not present in response", name)

	case strings.HasPrefix(lower, "response.body."):
		keyPath := path[len("response.body."):] // preserve original case for JSON keys
		return extractJSONPath(resp.Body, keyPath)

	default:
		return "", fmt.Errorf("unknown extraction path prefix: %q", path)
	}
}

// extractJSONPath extracts a value from JSON bytes following a dot-separated
// key path (e.g. "access_token" or "data.user.id").
//
// Only string, number, and boolean leaf values are supported; objects and
// arrays are returned as their JSON encoding.
func extractJSONPath(body []byte, keyPath string) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("response body is empty")
	}

	// Unmarshal into a generic map.
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return "", fmt.Errorf("response body is not valid JSON: %w", err)
	}

	keys := strings.Split(keyPath, ".")
	current := root

	for i, key := range keys {
		switch node := current.(type) {
		case map[string]interface{}:
			val, ok := node[key]
			if !ok {
				return "", fmt.Errorf("key %q not found at path segment %d", key, i+1)
			}
			current = val

		case []interface{}:
			// Allow numeric index access on arrays.
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(node) {
				return "", fmt.Errorf("index %q is out of range for array at path segment %d", key, i+1)
			}
			current = node[idx]

		default:
			return "", fmt.Errorf("cannot descend into %T at path segment %d (key %q)", current, i+1, key)
		}
	}

	return jsonValueToString(current)
}

// jsonValueToString converts a JSON-decoded Go value to its string
// representation suitable for variable storage.
func jsonValueToString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case float64:
		// Represent integers without a decimal point.
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10), nil
		}
		return strconv.FormatFloat(val, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(val), nil
	case nil:
		return "", nil
	default:
		// For objects and arrays, return their JSON encoding.
		b, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("cannot encode value as string: %w", err)
		}
		return string(b), nil
	}
}
