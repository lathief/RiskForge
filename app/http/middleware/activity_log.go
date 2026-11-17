package middleware

import (
	"bytes"
	"io"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/auth"
	"goravel/app/http/ctxkeys"
	"goravel/app/platform/logging"
)

// ActivityLog writes one activity.log entry per request: request and response headers and
// bodies (redacted), status, latency, live heap size, the authenticated user, and the
// gateway headers X-App, X-Path-Gateway, X-Request-From, X-Request-User, X-Device-ID,
// X-Request-Tags. It must run after RequestID so that the correlation ID is known.
//
// A panicking handler is logged as status 500 and the panic is re-raised so that Recover
// still answers the client and writes error.log.
type ActivityLog struct{}

func NewActivityLog() *ActivityLog {
	return &ActivityLog{}
}

func (m *ActivityLog) Signature() string {
	return "activity_log"
}

func (m *ActivityLog) Handle(ctx http.Context) {
	start := time.Now()
	logger := logging.Default()
	cfg := logger.Config()

	var requestBody []byte
	if cfg.LogHTTPBodies {
		requestBody = readRequestBody(ctx, cfg.MaxBodyBytes)
	}

	defer func() {
		if r := recover(); r != nil {
			m.write(ctx, logger, start, requestBody, http.StatusInternalServerError, false)
			panic(r)
		}
	}()

	ctx.Request().Next()

	m.write(ctx, logger, start, requestBody, ctx.Response().Origin().Status(), cfg.LogHTTPBodies)
}

func (m *ActivityLog) write(ctx http.Context, logger *logging.Logger, start time.Time, requestBody []byte, status int, withResponse bool) {
	cfg := logger.Config()
	req := ctx.Request()

	entry := logging.ActivityEntry{
		Host:           req.Host(),
		ClientIP:       req.Ip(),
		Referer:        req.Header("Referer"),
		ClientApp:      req.Header("X-App"),
		Method:         req.Method(),
		Path:           req.Path(),
		PathAlias:      req.Header("X-Path-Gateway"),
		RequestFrom:    req.Header("X-Request-From"),
		RequestUser:    req.Header("X-Request-User"),
		DeviceID:       req.Header("X-Device-ID"),
		RequestTags:    req.Header("X-Request-Tags"),
		RequestHeaders: logging.RedactHeaders(req.Headers()),
		HTTPCode:       status,
		ResponseTime:   float64(time.Since(start).Microseconds()) / 1000,
		MemoryUsage:    logging.HeapBytes(),
		RequestAt:      start.UTC().Format(time.RFC3339Nano),
	}
	entry.RequestID, _ = ctx.Value(ctxkeys.RequestID).(string)
	entry.Module, _ = ctx.Value(ctxkeys.Module).(string)
	if p, ok := auth.PrincipalFrom(ctx); ok {
		entry.UserID = p.UserID
	}
	if cfg.LogHTTPBodies {
		entry.RequestBody = logging.RedactBody(requestBody, cfg.MaxBodyBytes)
	}
	if withResponse {
		origin := ctx.Response().Origin()
		entry.ResponseHeaders = logging.RedactHeaders(origin.Header())
		// The buffer aliases Fiber's pooled response; RedactBody copies what it keeps.
		entry.ResponseBody = logging.RedactBody(origin.Body().Bytes(), cfg.MaxBodyBytes)
	}
	logger.Activity(entry)
}

// readRequestBody returns at most max+1 bytes, enough for RedactBody to detect an
// oversized body without buffering it whole. Origin() builds a fresh reader each call,
// so the handler can still read the body.
func readRequestBody(ctx http.Context, max int) []byte {
	origin := ctx.Request().Origin()
	if origin == nil || origin.Body == nil {
		return nil
	}
	defer origin.Body.Close()
	body, err := io.ReadAll(io.LimitReader(origin.Body, int64(max)+1))
	if err != nil {
		return nil
	}
	return bytes.Clone(body)
}
