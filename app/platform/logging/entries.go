package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"time"
)

// ActivityEntry is one HTTP request. Field names follow the team's gateway log format.
type ActivityEntry struct {
	Type            string            `json:"type"`
	Level           string            `json:"level"`
	AppName         string            `json:"app_name"`
	Env             string            `json:"env"`
	Module          string            `json:"module"`
	Host            string            `json:"host"`
	ClientIP        string            `json:"clientip"`
	Referer         string            `json:"referer"`
	ClientApp       string            `json:"clientapp"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	PathAlias       string            `json:"path_alias"`
	RequestID       string            `json:"requestID"`
	RequestFrom     string            `json:"requestFrom"`
	RequestUser     string            `json:"requestUser"`
	UserID          string            `json:"userID"`
	DeviceID        string            `json:"deviceID"`
	RequestTags     string            `json:"requestTags"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	RequestBody     json.RawMessage   `json:"requestBody"`
	ResponseBody    json.RawMessage   `json:"responseBody"`
	ResponseTime    float64           `json:"responseTime"` // milliseconds
	HTTPCode        int               `json:"httpCode"`
	MemoryUsage     uint64            `json:"memoryUsage"` // live heap bytes
	RequestAt       string            `json:"requestAt"`
}

// Activity writes an HTTP request entry. Level is derived from the status code.
func (l *Logger) Activity(e ActivityEntry) {
	e.Type = TypeActivity
	e.AppName, e.Env = l.cfg.AppName, l.cfg.Env
	switch {
	case e.HTTPCode >= 500:
		e.Level = "error"
	case e.HTTPCode >= 400:
		e.Level = "warn"
	default:
		e.Level = "info"
	}
	if e.Module == "" {
		e.Module = ModulePlatform
	}
	if e.RequestBody == nil {
		e.RequestBody = json.RawMessage("null")
	}
	if e.ResponseBody == nil {
		e.ResponseBody = json.RawMessage("null")
	}
	l.write(TypeActivity, e)
}

// ErrorEntry is a handled error, warning, or recovered panic.
type ErrorEntry struct {
	Type      string `json:"type"`
	Level     string `json:"level"`
	AppName   string `json:"app_name"`
	Env       string `json:"env"`
	Module    string `json:"module"`
	RequestID string `json:"requestID"`
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
	Panic     bool   `json:"panic"`
	Stack     string `json:"stack,omitempty"`
	At        string `json:"at"`
}

// Error records a handled error. ctx supplies requestID and module.
func (l *Logger) Error(ctx context.Context, message string, err error) {
	l.writeError(ctx, "error", message, err)
}

// Warn records a condition that needs attention but was handled (e.g. overload).
func (l *Logger) Warn(ctx context.Context, message string, err error) {
	l.writeError(ctx, "warn", message, err)
}

func (l *Logger) writeError(ctx context.Context, level, message string, err error) {
	e := ErrorEntry{
		Level:     level,
		Module:    ModuleFrom(ctx),
		RequestID: RequestIDFrom(ctx),
		Message:   message,
	}
	if err != nil {
		e.Error = err.Error()
	}
	l.writeErrorEntry(e)
}

// Panic records a recovered panic with its stack trace. The request itself (headers, body)
// is deliberately not included.
func (l *Logger) Panic(ctx context.Context, method, path string, value any) {
	l.writeErrorEntry(ErrorEntry{
		Level:     "error",
		Module:    ModuleFrom(ctx),
		RequestID: RequestIDFrom(ctx),
		Method:    method,
		Path:      path,
		Message:   "panic recovered",
		Error:     strings.TrimSpace(sprint(value)),
		Panic:     true,
		Stack:     string(debug.Stack()),
	})
}

func (l *Logger) writeErrorEntry(e ErrorEntry) {
	e.Type = TypeError
	e.AppName, e.Env = l.cfg.AppName, l.cfg.Env
	e.At = l.timestamp()
	l.write(TypeError, e)
}

// LifetimeEntry is a process lifecycle event.
type LifetimeEntry struct {
	Type          string  `json:"type"`
	Level         string  `json:"level"`
	AppName       string  `json:"app_name"`
	Env           string  `json:"env"`
	Module        string  `json:"module"`
	Event         string  `json:"event"`
	PID           int     `json:"pid"`
	Hostname      string  `json:"hostname"`
	GoVersion     string  `json:"goVersion"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	Detail        string  `json:"detail,omitempty"`
	MemoryUsage   uint64  `json:"memoryUsage"`
	At            string  `json:"at"`
}

var processStart = time.Now()

// Lifetime records a lifecycle event such as "starting", "signal_received", or "stopped".
func (l *Logger) Lifetime(event, detail string) {
	host, _ := os.Hostname()
	level := "info"
	if event == "crashed" || event == "startup_failed" {
		level = "error"
	}
	l.write(TypeLifetime, LifetimeEntry{
		Type:          TypeLifetime,
		Level:         level,
		AppName:       l.cfg.AppName,
		Env:           l.cfg.Env,
		Module:        ModulePlatform,
		Event:         event,
		PID:           os.Getpid(),
		Hostname:      host,
		GoVersion:     runtime.Version(),
		UptimeSeconds: time.Since(processStart).Seconds(),
		Detail:        detail,
		MemoryUsage:   HeapBytes(),
		At:            l.timestamp(),
	})
}

// QueryEntry is one SQL statement or transaction outcome. Argument values are never logged
// (they can contain password hashes or personal data); only their count.
type QueryEntry struct {
	Type          string  `json:"type"`
	Level         string  `json:"level"`
	AppName       string  `json:"app_name"`
	Env           string  `json:"env"`
	Module        string  `json:"module"`
	RequestID     string  `json:"requestID"`
	Kind          string  `json:"kind"` // select, insert, update, delete, statement, transaction
	SQL           string  `json:"sql"`
	Args          int     `json:"args"`
	Rows          int64   `json:"rows"`
	DurationMs    float64 `json:"durationMs"`
	Slow          bool    `json:"slow"`
	InTransaction bool    `json:"inTransaction"`
	Error         string  `json:"error,omitempty"`
	At            string  `json:"at"`
}

// maxSQLLength bounds the logged statement text.
const maxSQLLength = 2000

// Query records a statement. module labels the repository that issued it.
func (l *Logger) Query(ctx context.Context, e QueryEntry, duration time.Duration, err error) {
	e.Type = TypeQuery
	e.AppName, e.Env = l.cfg.AppName, l.cfg.Env
	if e.Module == "" {
		e.Module = ModulePlatform
	}
	e.RequestID = RequestIDFrom(ctx)
	e.SQL = compactSQL(e.SQL)
	e.DurationMs = float64(duration.Microseconds()) / 1000
	e.Slow = l.cfg.SlowQuery > 0 && duration >= l.cfg.SlowQuery
	e.Level = "info"
	if e.Slow {
		e.Level = "warn"
	}
	if err != nil {
		e.Error = err.Error()
		e.Level = "error"
	}
	e.At = l.timestamp()
	l.write(TypeQuery, e)
}

func compactSQL(sql string) string {
	s := strings.Join(strings.Fields(sql), " ")
	if len(s) > maxSQLLength {
		s = s[:maxSQLLength] + "...(truncated)"
	}
	return s
}

// HeapBytes returns the bytes occupied by live heap objects. It uses runtime/metrics,
// which unlike runtime.ReadMemStats does not stop the world.
func HeapBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

func sprint(v any) string {
	return fmt.Sprint(v)
}
