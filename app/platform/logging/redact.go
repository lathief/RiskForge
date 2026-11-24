package logging

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

// Redacted replaces sensitive values.
const Redacted = "[REDACTED]"

// sensitiveKeyParts: a JSON key or header whose lower-cased name contains any of these is
// redacted. Matching by substring also covers variants such as new_password or X-Api-Token.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"api_key", "apikey", "api-key", "credential", "private_key",
	"session", "bearer", "signature", "jwt", "passphrase", "auth_code",
	"cvv", "cvc", "card_number", "cardnumber", "account_number",
}

// sensitiveExactKeys are too short to match by substring ("pin" would hit "shipping",
// "auth" would hit "author", "pass" would hit "passenger"). "nik" is the Indonesian
// national identity number.
var sensitiveExactKeys = map[string]bool{
	"pin": true, "otp": true, "auth": true, "pass": true, "pwd": true, "nik": true, "pan": true,
}

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
	Bytes   int    `json:"_bytes,omitempty"`
}

// withheldBodyPrefixes are the route prefixes whose request and response bodies are never
// written to activity.log. The Decision API carries the applicant's financial data (income,
// installments, credit history) and the decision made from it. Key-based redaction cannot
// recognise such values (`monthly_income` is not a secret by name), and the data dictionary is
// configurable, so the whole body is left out and only the metadata (user, status, duration,
// request id) is logged. The stored decision in the database is the record of the input.
//
// The audit search of decisions (RF-022) lists the same results by application, so it is
// withheld too.
var withheldBodyPrefixes = []string{"/api/v1/decisions", "/api/v1/audit/decisions"}

// simulationPrefix and simulationSuffix delimit /api/v1/policy-versions/{version_id}/simulate
// (RF-017). The simulator takes the same applicant data as a decision and returns its trace, so
// its bodies are withheld too. The other policy-version routes carry policy configuration, not
// applicant data, and stay logged.
const (
	simulationPrefix = "/api/v1/policy-versions/"
	simulationSuffix = "/simulate"
)

// normalizePath folds the variants the router treats as the same route: Fiber matches paths
// case-insensitively and ignores a trailing slash by default, so "/API/v1/Decisions/evaluate/"
// reaches the same handler as "/api/v1/decisions/evaluate" and must be withheld the same way
// (M4 review).
func normalizePath(path string) string {
	p := strings.ToLower(path)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// BodyWithheld reports whether bodies of requests to path must not be logged.
func BodyWithheld(path string) bool {
	path = normalizePath(path)
	for _, p := range withheldBodyPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	// Any path of that shape is withheld, also one with an empty or malformed id that no route
	// matches: the body of such a request is still an applicant, sent to the wrong address.
	if len(path) >= len(simulationPrefix)+len(simulationSuffix) &&
		strings.HasPrefix(path, simulationPrefix) && strings.HasSuffix(path, simulationSuffix) {
		return true
	}
	return false
}

// WithheldBody is the value logged in place of a withheld body.
func WithheldBody() json.RawMessage {
	return mustMarshal(BodySummary{Omitted: "sensitive_route"})
}

// RedactBody returns a JSON value safe to log for body:
//   - empty body: null
//   - valid JSON up to maxBytes: the same JSON with sensitive keys replaced at any depth
//   - anything else (too large, not JSON): only its size, because a partial or non-JSON
//     body cannot be redacted reliably and may contain credentials
func RedactBody(body []byte, maxBytes int) json.RawMessage {
	return RedactBodySized(body, len(body), maxBytes)
}

// RedactBodySized is RedactBody for a body that may have been read only partially: size is
// the full body size (for example Content-Length) and is what gets reported when omitted.
func RedactBodySized(body []byte, size, maxBytes int) json.RawMessage {
	if size < len(body) {
		size = len(body)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 && size <= len(body) {
		return json.RawMessage("null")
	}
	if size > maxBytes {
		return mustMarshal(BodySummary{Omitted: "too_large", Bytes: size})
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil || dec.More() {
		return mustMarshal(BodySummary{Omitted: "not_json", Bytes: size})
	}
	return mustMarshal(redactValue(v))
}

// maxFieldValue bounds a single logged scalar such as a gateway header or path.
const maxFieldValue = 512

// Cap truncates s to maxFieldValue bytes on a UTF-8 boundary.
func Cap(s string) string {
	if len(s) <= maxFieldValue {
		return s
	}
	cut := maxFieldValue
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}

// SafeURL drops the query string and fragment of a URL (they can carry tokens, for example
// a Referer of "https://app/cb?code=...") and any "user:password@" credentials, then caps
// the length.
func SafeURL(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		rest := raw[scheme+3:]
		host := rest
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			host = rest[:slash]
		}
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			raw = raw[:scheme+3] + rest[at+1:]
		}
	}
	return Cap(raw)
}

// RedactMap returns a copy of m in which the value of every sensitive key, at any depth, is
// replaced by Redacted. The audit search uses it so a stored event cannot return a secret even
// if a writer put one there by mistake (RF-022).
func RedactMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return redactValue(m).(map[string]any)
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
		if canonical == "Referer" {
			value = SafeURL(value) // query strings and fragments can carry tokens
		}
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
