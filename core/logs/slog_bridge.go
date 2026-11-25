// Copyright 2024 beego Author. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AdapterSlog registers the standard library log/slog-backed adapter.
const AdapterSlog = "slog"

type ctxKey string

const requestIDContextKey ctxKey = "beego.logs.request_id"

func init() {
	Register(AdapterSlog, NewSlogLogger)
}

type slogAdapter struct {
	Level     int    `json:"level"`
	Format    string `json:"format"`
	writer    io.Writer
	logger    *slog.Logger
	formatter LogFormatter
}

// NewSlogLogger creates a Logger backed by Go standard library log/slog.
func NewSlogLogger() Logger {
	return newSlogLoggerWithWriter(os.Stdout)
}

func newSlogLoggerWithWriter(w io.Writer) *slogAdapter {
	if w == nil {
		w = os.Stdout
	}
	return &slogAdapter{
		Level:  LevelDebug,
		Format: "text",
		writer: w,
	}
}

func (s *slogAdapter) Init(config string) error {
	if strings.TrimSpace(config) != "" {
		if err := json.Unmarshal([]byte(config), s); err != nil {
			return err
		}
	}
	if s.writer == nil {
		s.writer = os.Stdout
	}
	opts := &slog.HandlerOptions{
		Level: toSlogLevel(s.Level),
	}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(s.Format), "json") {
		handler = slog.NewJSONHandler(s.writer, opts)
	} else {
		handler = slog.NewTextHandler(s.writer, opts)
	}
	s.logger = slog.New(handler)
	return nil
}

func (s *slogAdapter) WriteMsg(lm *LogMsg) error {
	if lm == nil || lm.Level > s.Level {
		return nil
	}
	if s.logger == nil {
		if err := s.Init(""); err != nil {
			return err
		}
	}

	msg := lm.Msg
	if len(lm.Args) > 0 {
		msg = fmt.Sprintf(lm.Msg, lm.Args...)
	}
	if s.formatter != nil {
		msg = s.formatter.Format(lm)
	}

	attrs := make([]any, 0, 3)
	if p := strings.TrimSpace(lm.Prefix); p != "" {
		attrs = append(attrs, slog.String("prefix", p))
	}
	if lm.enableFuncCallDepth && lm.FilePath != "" {
		fp := lm.FilePath
		if !lm.enableFullFilePath {
			fp = filepath.Base(fp)
		}
		attrs = append(attrs, slog.String("source", fmt.Sprintf("%s:%d", fp, lm.LineNumber)))
	}

	s.logger.Log(context.Background(), toSlogLevel(lm.Level), msg, attrs...)
	return nil
}

func (s *slogAdapter) Destroy() {}

func (s *slogAdapter) Flush() {}

func (s *slogAdapter) SetFormatter(f LogFormatter) {
	s.formatter = f
}

func toSlogLevel(lvl int) slog.Level {
	switch {
	case lvl <= LevelError:
		return slog.LevelError
	case lvl <= LevelNotice:
		return slog.LevelWarn
	case lvl == LevelInfo:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

func fromSlogLevel(lvl slog.Level) int {
	switch {
	case lvl >= slog.LevelError:
		return LevelError
	case lvl >= slog.LevelWarn:
		return LevelWarn
	case lvl >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}

var _ slog.Handler = (*BeeLogger)(nil)

// Enabled implements slog.Handler.
func (bl *BeeLogger) Enabled(_ context.Context, level slog.Level) bool {
	return fromSlogLevel(level) <= bl.level
}

// Handle implements slog.Handler.
func (bl *BeeLogger) Handle(ctx context.Context, r slog.Record) error {
	return (&beeSlogHandler{bl: bl}).Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (bl *BeeLogger) WithAttrs(attrs []slog.Attr) slog.Handler {
	return (&beeSlogHandler{bl: bl}).WithAttrs(attrs)
}

// WithGroup implements slog.Handler.
func (bl *BeeLogger) WithGroup(name string) slog.Handler {
	return (&beeSlogHandler{bl: bl}).WithGroup(name)
}

// AsSlog wraps the global BeeLogger as a standard *slog.Logger.
func AsSlog() *slog.Logger {
	return beeLogger.AsSlog()
}

// AsSlog wraps this BeeLogger as a standard *slog.Logger.
func (bl *BeeLogger) AsSlog() *slog.Logger {
	return slog.New(bl)
}

type beeSlogHandler struct {
	bl     *BeeLogger
	attrs  []slog.Attr
	groups []string
}

func (h *beeSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.bl.Enabled(ctx, level)
}

func (h *beeSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if !h.Enabled(ctx, r.Level) {
		return nil
	}
	var pairs []string
	if reqID := RequestIDFromContext(ctx); reqID != "" {
		pairs = append(pairs, "request_id="+reqID)
	}
	prefix := strings.Join(h.groups, ".")
	for _, a := range h.attrs {
		pairs = append(pairs, formatSlogAttr(prefix, a)...)
	}
	r.Attrs(func(a slog.Attr) bool {
		pairs = append(pairs, formatSlogAttr(prefix, a)...)
		return true
	})

	msg := r.Message
	if len(pairs) > 0 {
		msg = msg + " " + strings.Join(pairs, " ")
	}
	lm := &LogMsg{
		Level: fromSlogLevel(r.Level),
		Msg:   msg,
		When:  r.Time,
	}
	if lm.When.IsZero() {
		lm.When = time.Now()
	}
	return h.bl.writeMsg(lm)
}

func (h *beeSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nextAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	nextAttrs = append(nextAttrs, h.attrs...)
	nextAttrs = append(nextAttrs, attrs...)
	nextGroups := append([]string(nil), h.groups...)
	return &beeSlogHandler{
		bl:     h.bl,
		attrs:  nextAttrs,
		groups: nextGroups,
	}
}

func (h *beeSlogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nextAttrs := append([]slog.Attr(nil), h.attrs...)
	nextGroups := append(append([]string(nil), h.groups...), name)
	return &beeSlogHandler{
		bl:     h.bl,
		attrs:  nextAttrs,
		groups: nextGroups,
	}
}

func formatSlogAttr(groupPrefix string, a slog.Attr) []string {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return nil
	}
	key := a.Key
	if groupPrefix != "" && key != "" {
		key = groupPrefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		var res []string
		for _, sub := range a.Value.Group() {
			res = append(res, formatSlogAttr(key, sub)...)
		}
		return res
	}
	return []string{fmt.Sprintf("%s=%v", key, a.Value.Any())}
}

// WithRequestID returns a child context carrying request_id for structured trace logs.
func WithRequestID(ctx context.Context, reqID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDContextKey, reqID)
}

// RequestIDFromContext extracts a request trace ID from context if present.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(requestIDContextKey).(string); ok && v != "" {
		return v
	}
	if v, ok := ctx.Value("request_id").(string); ok && v != "" {
		return v
	}
	if v, ok := ctx.Value("X-Request-Id").(string); ok && v != "" {
		return v
	}
	return ""
}

// InfoCtx logs an INFO message with request_id extracted from ctx and structured key=value fields.
func InfoCtx(ctx context.Context, msg string, kvArgs ...any) {
	beeLogger.InfoCtx(ctx, msg, kvArgs...)
}

// WarnCtx logs a WARN message with request_id extracted from ctx and structured key=value fields.
func WarnCtx(ctx context.Context, msg string, kvArgs ...any) {
	beeLogger.WarnCtx(ctx, msg, kvArgs...)
}

// ErrorCtx logs an ERROR message with request_id extracted from ctx and structured key=value fields.
func ErrorCtx(ctx context.Context, msg string, kvArgs ...any) {
	beeLogger.ErrorCtx(ctx, msg, kvArgs...)
}

// DebugCtx logs a DEBUG message with request_id extracted from ctx and structured key=value fields.
func DebugCtx(ctx context.Context, msg string, kvArgs ...any) {
	beeLogger.DebugCtx(ctx, msg, kvArgs...)
}

// InfoCtx logs an INFO message with request_id extracted from ctx and structured key=value fields.
func (bl *BeeLogger) InfoCtx(ctx context.Context, msg string, kvArgs ...any) {
	bl.logCtx(LevelInfo, ctx, msg, kvArgs...)
}

// WarnCtx logs a WARN message with request_id extracted from ctx and structured key=value fields.
func (bl *BeeLogger) WarnCtx(ctx context.Context, msg string, kvArgs ...any) {
	bl.logCtx(LevelWarn, ctx, msg, kvArgs...)
}

// ErrorCtx logs an ERROR message with request_id extracted from ctx and structured key=value fields.
func (bl *BeeLogger) ErrorCtx(ctx context.Context, msg string, kvArgs ...any) {
	bl.logCtx(LevelError, ctx, msg, kvArgs...)
}

// DebugCtx logs a DEBUG message with request_id extracted from ctx and structured key=value fields.
func (bl *BeeLogger) DebugCtx(ctx context.Context, msg string, kvArgs ...any) {
	bl.logCtx(LevelDebug, ctx, msg, kvArgs...)
}

func (bl *BeeLogger) logCtx(level int, ctx context.Context, msg string, kvArgs ...any) {
	if level > bl.level {
		return
	}
	formatted := formatCtxStructuredMsg(ctx, msg, kvArgs...)
	bl.writeMsg(&LogMsg{
		Level: level,
		Msg:   formatted,
		When:  time.Now(),
	})
}

func formatCtxStructuredMsg(ctx context.Context, msg string, kvArgs ...any) string {
	var parts []string
	if reqID := RequestIDFromContext(ctx); reqID != "" {
		parts = append(parts, "request_id="+reqID)
	}
	for i := 0; i < len(kvArgs); i += 2 {
		key := fmt.Sprint(kvArgs[i])
		if i+1 < len(kvArgs) {
			parts = append(parts, fmt.Sprintf("%s=%v", key, kvArgs[i+1]))
		} else {
			parts = append(parts, fmt.Sprintf("!BADKEY=%v", kvArgs[i]))
		}
	}
	if len(parts) == 0 {
		return msg
	}
	return msg + " " + strings.Join(parts, " ")
}
