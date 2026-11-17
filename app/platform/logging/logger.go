// Package logging writes structured JSON-lines logs, one file per log type:
//
//	activity.log  one entry per HTTP request: request/response headers and bodies (redacted)
//	error.log     panics and handled errors/warnings
//	lifetime.log  process lifecycle: starting, signal received, stopped
//	query.log     SQL statements and transactions with their duration
//
// Every entry carries "type", "module", and (when available) "requestID" so the four files
// can be joined. The package is framework-agnostic; HTTP and database adapters feed it.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Log types, also the file names without extension.
const (
	TypeActivity = "activity"
	TypeError    = "error"
	TypeLifetime = "lifetime"
	TypeQuery    = "query"
)

// Config controls where and what is logged.
type Config struct {
	AppName string
	Env     string
	// Dir holds the four log files. Empty disables file output.
	Dir string
	// Stdout mirrors every entry to standard output (for `docker logs`).
	Stdout bool
	// LogHTTPBodies includes redacted request/response bodies in activity.log.
	LogHTTPBodies bool
	// MaxBodyBytes: bodies larger than this are logged by size only.
	MaxBodyBytes int
	// SlowQuery marks queries at or above this duration with "slow": true.
	SlowQuery time.Duration
}

// Logger writes entries. All methods are safe for concurrent use.
type Logger struct {
	cfg   Config
	mu    sync.Mutex
	sinks map[string]io.Writer
	files []*os.File
	now   func() time.Time
}

// New opens (or creates) the log files in cfg.Dir in append mode.
func New(cfg Config) (*Logger, error) {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 8 * 1024
	}
	l := &Logger{cfg: cfg, sinks: map[string]io.Writer{}, now: time.Now}
	for _, t := range []string{TypeActivity, TypeError, TypeLifetime, TypeQuery} {
		var writers []io.Writer
		if cfg.Dir != "" {
			if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
				return nil, fmt.Errorf("create log dir: %w", err)
			}
			f, err := os.OpenFile(filepath.Join(cfg.Dir, t+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
			if err != nil {
				_ = l.Close()
				return nil, fmt.Errorf("open %s.log: %w", t, err)
			}
			l.files = append(l.files, f)
			writers = append(writers, f)
		}
		if cfg.Stdout {
			writers = append(writers, os.Stdout)
		}
		l.sinks[t] = io.MultiWriter(writers...)
	}
	return l, nil
}

// Discard returns a logger that writes nothing (used before Init and in unit tests).
func Discard() *Logger {
	return &Logger{
		cfg:   Config{MaxBodyBytes: 8 * 1024},
		sinks: map[string]io.Writer{TypeActivity: io.Discard, TypeError: io.Discard, TypeLifetime: io.Discard, TypeQuery: io.Discard},
		now:   time.Now,
	}
}

// NewWithWriters builds a logger over arbitrary writers, keyed by log type (for tests).
func NewWithWriters(cfg Config, writers map[string]io.Writer) *Logger {
	l := Discard()
	l.cfg = cfg
	if l.cfg.MaxBodyBytes <= 0 {
		l.cfg.MaxBodyBytes = 8 * 1024
	}
	for t, w := range writers {
		l.sinks[t] = w
	}
	return l
}

// Config returns the logger configuration.
func (l *Logger) Config() Config { return l.cfg }

// Close closes the log files.
func (l *Logger) Close() error {
	var first error
	for _, f := range l.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// write marshals entry as one JSON line. A marshalling failure is reported on stderr
// rather than dropped silently, and never panics the caller.
func (l *Logger) write(logType string, entry any) {
	line, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging: cannot encode %s entry: %v\n", logType, err)
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.sinks[logType].Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "logging: cannot write %s entry: %v\n", logType, err)
	}
}

func (l *Logger) timestamp() string {
	return l.now().UTC().Format(time.RFC3339Nano)
}

var (
	defaultMu     sync.RWMutex
	defaultLogger = Discard()
)

// SetDefault installs the process-wide logger.
func SetDefault(l *Logger) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultLogger = l
}

// Default returns the process-wide logger; a discarding logger until SetDefault is called.
func Default() *Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLogger
}
