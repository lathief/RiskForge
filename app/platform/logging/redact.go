package logging

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// Redacted replaces sensitive values.
const Redacted = "[REDACTED]"

// sensitiveKeyParts: a JSON key or header whose lower-cased name contains any of these is
// redacted. Matching by substring also covers variants such as new_password or X-Api-Token.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"api_key", "apikey", "api-key", "credential", "private_key",
}

// sensitiveExactKeys are too short to match by substring ("pin" would hit "shipping").
var sensitiveExactKeys = map[string]bool{"pin": true, "otp": true}

// nonSensitiveKeys match a sensitive substring but carry no secret.
var nonSensitiveKeys = map[string]bool{"token_type": true}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	if nonSensitiveKeys[k] {
		return false
	}
	if sensitiveExactKeys[k] {
		return true
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// BodySummary is logged instead of a body that cannot be redacted safely.
type BodySummary struct {
	Omitted string `json:"_omitted"`
	Bytes   int    `json:"_bytes"`
}

// RedactBody returns a JSON value safe to log for body:
//   - empty body: null
//   - valid JSON up to maxBytes: the same JSON with sensitive keys replaced at any depth
//   - anything else (too large, not JSON): only its size, because a partial or non-JSON
//     body cannot be redacted reliably and may contain credentials
func RedactBody(body []byte, maxBytes int) json.RawMessage {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	if len(body) > maxBytes {
		return mustMarshal(BodySummary{Omitted: "too_large", Bytes: len(body)})
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil || dec.More() {
		return mustMarshal(BodySummary{Omitted: "not_json", Bytes: len(body)})
	}
	return mustMarshal(redactValue(v))
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSensitiveKey(k) {
				out[k] = Redacted
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactValue(val)
		}
		return out
	default:
		return v
	}
}

// maxHeaderValue bounds a single logged header value.
const maxHeaderValue = 512

// RedactHeaders flattens headers to name -> value (multiple values joined by ", "),
// redacting sensitive headers and truncating long values.
func RedactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		canonical := http.CanonicalHeaderKey(name)
		if isSensitiveKey(canonical) {
			out[canonical] = Redacted
			continue
		}
		value := strings.Join(h[name], ", ")
		if len(value) > maxHeaderValue {
			value = value[:maxHeaderValue] + "...(truncated)"
		}
		out[canonical] = value
	}
	return out
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"_omitted":"unencodable"}`)
	}
	return b
}
