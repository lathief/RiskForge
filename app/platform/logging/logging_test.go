package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

func TestRedactBodyNestedAndCaseInsensitive(t *testing.T) {
	body := []byte(`{"username":"demo","Password":"p1","profile":{"api_key":"k","shipping":"keep","pin":"1234"},
		"items":[{"refresh_token":"t"},{"name":"ok"}],"access_token":"jwt","token_type":"Bearer","amount":12000000.50}`)
	raw := RedactBody(body, 8192)
	assert.Contains(t, string(raw), `"amount":12000000.50`, "numbers keep their exact text")
	got := decode(t, raw).(map[string]any)
	assert.Equal(t, "Bearer", got["token_type"], "token_type is not a secret")

	assert.Equal(t, "demo", got["username"])
	assert.Equal(t, Redacted, got["Password"])
	assert.Equal(t, Redacted, got["access_token"])
	profile := got["profile"].(map[string]any)
	assert.Equal(t, Redacted, profile["api_key"])
	assert.Equal(t, Redacted, profile["pin"])
	assert.Equal(t, "keep", profile["shipping"], "substring 'pin' must not redact unrelated keys")
	items := got["items"].([]any)
	assert.Equal(t, Redacted, items[0].(map[string]any)["refresh_token"])
	assert.Equal(t, "ok", items[1].(map[string]any)["name"])
}

func TestRedactBodyNeverLogsUnparseableOrOversizedContent(t *testing.T) {
	for name, body := range map[string]string{
		"broken json": `{"password":"secret-1"`,
		"form":        `password=secret-1&user=a`,
		"two values":  `{"a":1} {"password":"secret-1"}`,
	} {
		raw := RedactBody([]byte(body), 8192)
		assert.NotContains(t, string(raw), "secret-1", name)
		assert.Equal(t, "not_json", decode(t, raw).(map[string]any)["_omitted"], name)
	}

	big := `{"password":"secret-1","pad":"` + strings.Repeat("x", 100) + `"}`
	raw := RedactBody([]byte(big), 50)
	assert.NotContains(t, string(raw), "secret-1")
	assert.Equal(t, "too_large", decode(t, raw).(map[string]any)["_omitted"])

	assert.Equal(t, "null", string(RedactBody(nil, 10)))
	assert.Equal(t, "null", string(RedactBody([]byte("  \n"), 10)))
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer abc")
	h.Set("Cookie", "s=1")
	h.Set("X-Api-Key", "k")
	h.Set("X-App", "los-simulator")
	h.Add("Accept", "a")
	h.Add("Accept", "b")
	h.Set("X-Long", strings.Repeat("v", 600))

	got := RedactHeaders(h)
	assert.Equal(t, Redacted, got["Authorization"])
	assert.Equal(t, Redacted, got["Cookie"])
	assert.Equal(t, Redacted, got["X-Api-Key"])
	assert.Equal(t, "los-simulator", got["X-App"])
	assert.Equal(t, "a, b", got["Accept"])
	assert.True(t, strings.HasSuffix(got["X-Long"], "...(truncated)"))
	assert.LessOrEqual(t, len(got["X-Long"]), maxHeaderValue+20)
}

func newBufferLogger(cfg Config) (*Logger, map[string]*bytes.Buffer) {
	bufs := map[string]*bytes.Buffer{}
	writers := map[string]io.Writer{}
	for _, t := range []string{TypeActivity, TypeError, TypeLifetime, TypeQuery} {
		bufs[t] = &bytes.Buffer{}
		writers[t] = bufs[t]
	}
	return NewWithWriters(cfg, writers), bufs
}

func lines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "every line must be valid JSON: %s", sc.Text())
		out = append(out, m)
	}
	return out
}

func TestEntriesGoToTheirOwnSinkWithCommonFields(t *testing.T) {
	l, bufs := newBufferLogger(Config{AppName: "RiskForge", Env: "testing", SlowQuery: 10 * time.Millisecond})
	ctx := context.WithValue(context.WithValue(context.Background(), RequestIDKey, "req-123"), ModuleKey, "catalog")

	l.Activity(ActivityEntry{HTTPCode: 503, RequestID: "req-123", Module: "catalog"})
	l.Error(ctx, "list failed", errors.New("db down"))
	l.Warn(ctx, "busy", nil)
	l.Panic(ctx, "POST", "/x", "boom")
	l.Lifetime("starting", "http=0.0.0.0:3000")
	l.Query(ctx, QueryEntry{Module: "catalog", Kind: "select", SQL: "SELECT  *\n  FROM t WHERE a = $1", Args: 1, Rows: 3}, 25*time.Millisecond, nil)

	act := lines(t, bufs[TypeActivity])
	require.Len(t, act, 1)
	assert.Equal(t, "activity", act[0]["type"])
	assert.Equal(t, "error", act[0]["level"], "5xx is level error")
	assert.Equal(t, "RiskForge", act[0]["app_name"])
	assert.Nil(t, act[0]["requestBody"])

	errs := lines(t, bufs[TypeError])
	require.Len(t, errs, 3)
	assert.Equal(t, "req-123", errs[0]["requestID"])
	assert.Equal(t, "catalog", errs[0]["module"])
	assert.Equal(t, "db down", errs[0]["error"])
	assert.Equal(t, "warn", errs[1]["level"])
	assert.Equal(t, true, errs[2]["panic"])
	assert.Contains(t, errs[2]["stack"], "goroutine")

	life := lines(t, bufs[TypeLifetime])
	require.Len(t, life, 1)
	assert.Equal(t, "starting", life[0]["event"])
	assert.NotZero(t, life[0]["pid"])

	q := lines(t, bufs[TypeQuery])
	require.Len(t, q, 1)
	assert.Equal(t, "SELECT * FROM t WHERE a = $1", q[0]["sql"], "whitespace compacted")
	assert.Equal(t, 25.0, q[0]["durationMs"])
	assert.Equal(t, true, q[0]["slow"])
	assert.Equal(t, "warn", q[0]["level"])
	assert.Equal(t, "req-123", q[0]["requestID"])
}

func TestConcurrentWritesProduceWholeLines(t *testing.T) {
	l, bufs := newBufferLogger(Config{AppName: "RiskForge"})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				l.Activity(ActivityEntry{HTTPCode: 200, Path: strings.Repeat("p", 200)})
			}
		}()
	}
	wg.Wait()
	assert.Len(t, lines(t, bufs[TypeActivity]), 1000)
}

func TestModuleAndRequestIDDefaults(t *testing.T) {
	assert.Equal(t, ModulePlatform, ModuleFrom(context.Background()))
	assert.Equal(t, "", RequestIDFrom(context.Background()))
	assert.Equal(t, ModulePlatform, ModuleFrom(nil)) //nolint:staticcheck // nil is handled deliberately
}

func TestLongSQLIsTruncated(t *testing.T) {
	s := compactSQL("SELECT " + strings.Repeat("a,", 3000))
	assert.True(t, strings.HasSuffix(s, "...(truncated)"))
	assert.LessOrEqual(t, len(s), maxSQLLength+20)
}

func TestDiscardLoggerIsSafeToUse(t *testing.T) {
	l := Discard()
	l.Activity(ActivityEntry{})
	l.Error(context.Background(), "x", nil)
	l.Lifetime("x", "")
	l.Query(context.Background(), QueryEntry{}, time.Millisecond, nil)
	assert.NoError(t, l.Close())
}
