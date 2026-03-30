package relay

import (
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Variable interpolation — resolves {{varName}} placeholders
// ─────────────────────────────────────────────────────────────────────────────

// Interpolate replaces every occurrence of {{varName}} in s with the
// corresponding value from vars.  Unknown variables are left as-is so that
// callers can detect unresolved placeholders when needed.
func Interpolate(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}

	var sb strings.Builder
	sb.Grow(len(s))

	i := 0
	for i < len(s) {
		start := strings.Index(s[i:], "{{")
		if start == -1 {
			sb.WriteString(s[i:])
			break
		}
		// Write everything before the opening "{{".
		sb.WriteString(s[i : i+start])
		i += start + 2 // skip past "{{"

		end := strings.Index(s[i:], "}}")
		if end == -1 {
			// Unmatched "{{" — write it literally and stop.
			sb.WriteString("{{")
			sb.WriteString(s[i:])
			break
		}

		varName := strings.TrimSpace(s[i : i+end])
		i += end + 2 // skip past "}}"

		if val, ok := vars[varName]; ok {
			sb.WriteString(val)
		} else {
			// Unknown variable — preserve the placeholder verbatim.
			sb.WriteString("{{")
			sb.WriteString(varName)
			sb.WriteString("}}")
		}
	}

	return sb.String()
}

// InterpolateBytes applies Interpolate to a byte slice that is expected to
// contain UTF-8 text (e.g. a JSON request body).  Binary content that contains
// a literal "{{" will be processed, which is intentional — the engine treats
// all request bodies as potentially templated text.
func InterpolateBytes(b []byte, vars map[string]string) []byte {
	if len(b) == 0 {
		return b
	}
	result := Interpolate(string(b), vars)
	return []byte(result)
}
