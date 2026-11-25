package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/auth"
	"github.com/lathief/RiskForge/app/http/ctxkeys"
	"github.com/lathief/RiskForge/app/platform/logging"
	"github.com/lathief/RiskForge/app/platform/metrics"
)

// ActivityLog writes one activity.log entry per request: request and response headers,
// bodies (redacted, only when LOG_HTTP_BODIES=true), status, latency, live heap size, the
// authenticated user, and the gateway headers X-App, X-Path-Gateway, X-Request-From,
// X-Request-User, X-Device-ID, X-Request-Tags. It runs after RequestID and inside the
// driver's request timeout.
//
// Timeouts: Fiber's timeout middleware runs the handler in its own goroutine. When the
// deadline passes it writes 408 to the request context at once and abandons it while the
// handler may still be running; any later access to that request or response races with
// the driver. Therefore everything about the request is captured before the handler runs,
// and after the deadline only that copy is used: the
// entry records 408 (what the client received) and never touches the request or response
// again. After the deadline the driver also stops calling Recover, so a late panic is
// written to error.log here. Handlers must honour ctx.Context() so they stop at the deadline.
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
	reqCtx := ctx.Context() // carries the request deadline

	entry := captureRequest(ctx, start)
	withheld := logging.BodyWithheld(ctx.Request().Path())
	switch {
	case cfg.LogHTTPBodies && withheld:
		entry.RequestBody = logging.WithheldBody()
	case cfg.LogHTTPBodies:
		body := readRequestBody(ctx, cfg.MaxBodyBytes)
		entry.RequestBody = logging.RedactBodySized(body.data, body.size, cfg.MaxBodyBytes)
	}

	defer func() {
		if r := recover(); r != nil {
			if deadlinePassed(reqCtx) {
				logger.Panic(reqCtx, entry.Method, entry.Path, r)
				finish(logger, entry, start, http.StatusRequestTimeout)
			} else {
				m.addUser(ctx, &entry)
				finish(logger, entry, start, http.StatusInternalServerError)
			}
			panic(r)
		}
	}()

	ctx.Request().Next()

	if deadlinePassed(reqCtx) {
		finish(logger, entry, start, http.StatusRequestTimeout)
		return
	}
	m.addUser(ctx, &entry)
	origin := ctx.Response().Origin()
	entry.ResponseHeaders = logging.RedactHeaders(origin.Header())
	switch {
	case cfg.LogHTTPBodies && withheld:
		entry.ResponseBody = logging.WithheldBody()
	case cfg.LogHTTPBodies:
		// The buffer aliases Fiber's pooled response; RedactBody copies what it keeps.
		entry.ResponseBody = logging.RedactBody(origin.Body().Bytes(), cfg.MaxBodyBytes)
	}
	finish(logger, entry, start, origin.Status())
}

func deadlinePassed(reqCtx context.Context) bool {
	return errors.Is(reqCtx.Err(), context.DeadlineExceeded)
}

// captureRequest copies every request-derived field before the handler runs.
func captureRequest(ctx http.Context, start time.Time) logging.ActivityEntry {
	req := ctx.Request()
	entry := logging.ActivityEntry{
		Host:           logging.Cap(req.Host()),
		ClientIP:       req.Ip(),
		Referer:        logging.SafeURL(req.Header("Referer")),
		ClientApp:      logging.Cap(req.Header("X-App")),
		Method:         req.Method(),
		Path:           logging.Cap(req.Path()),
		PathAlias:      logging.Cap(req.Header("X-Path-Gateway")),
		RequestFrom:    logging.Cap(req.Header("X-Request-From")),
		RequestUser:    logging.Cap(req.Header("X-Request-User")),
		DeviceID:       logging.Cap(req.Header("X-Device-ID")),
		RequestTags:    logging.Cap(req.Header("X-Request-Tags")),
		RequestHeaders: logging.RedactHeaders(req.Headers()),
		RequestAt:      start.UTC().Format(time.RFC3339Nano),
	}
	entry.RequestID, _ = ctx.Value(ctxkeys.RequestID).(string)
	return entry
}

// addUser reads values set by later middleware (module label, authenticated user). It is
// only called before the deadline, while the request context is still this request's.
func (m *ActivityLog) addUser(ctx http.Context, entry *logging.ActivityEntry) {
	entry.Module, _ = ctx.Value(ctxkeys.Module).(string)
	if p, ok := auth.PrincipalFrom(ctx); ok {
		entry.UserID = p.UserID
	}
}

func finish(logger *logging.Logger, entry logging.ActivityEntry, start time.Time, status int) {
	entry.HTTPCode = status
	metrics.ObserveHTTP(entry.Module, status, time.Since(start))
	entry.ResponseTime = float64(time.Since(start).Microseconds()) / 1000
	entry.MemoryUsage = logging.HeapBytes()
	logger.Activity(entry)
}

// requestBody holds at most max+1 bytes of the body plus its declared total size.
type requestBody struct {
	data []byte
	size int
}

// readRequestBody reads at most max+1 bytes, enough for redaction to detect an oversized
// body without buffering it whole. Origin() builds a fresh reader each call, so the handler
// can still read the body.
//
// Origin() converts the request to net/http and panics on a URL it cannot parse (for
// example "/%zz"). Such a request is still served (routing works on the raw path); only
// its body is left out of the log.
func readRequestBody(ctx http.Context, max int) requestBody {
	return readBody(ctx.Request().Origin, max)
}

// readBody takes the request constructor as a function so the panic path can be tested.
func readBody(origin func() *nethttp.Request, max int) (out requestBody) {
	defer func() {
		if recover() != nil {
			out = requestBody{}
		}
	}()
	return readOrigin(origin(), max)
}

func readOrigin(origin *nethttp.Request, max int) requestBody {
	if origin == nil || origin.Body == nil {
		return requestBody{}
	}
	defer origin.Body.Close()
	data, err := io.ReadAll(io.LimitReader(origin.Body, int64(max)+1))
	if err != nil {
		return requestBody{}
	}
	size := len(data)
	if origin.ContentLength > int64(size) {
		size = int(origin.ContentLength)
	}
	return requestBody{data: bytes.Clone(data), size: size}
}
