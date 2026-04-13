package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const (
	colorReset  = "\033[0m"
	colorGray   = "\033[90m"
	colorBlue   = "\033[34m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
)

type prettyHandler struct {
	writer io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	group  string
}

func NewLogger() *slog.Logger {
	return slog.New(&prettyHandler{
		writer: os.Stdout,
		level:  slog.LevelInfo,
	})
}

func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *prettyHandler) Handle(_ context.Context, record slog.Record) error {
	parts := make([]string, 0, 6)
	parts = append(parts, colorize(colorGray, record.Time.Format("15:04:05.000")))
	parts = append(parts, colorize(levelColor(record.Level), padLevel(record.Level.String())))
	parts = append(parts, colorize(colorCyan, record.Message))

	attrs := make([]slog.Attr, 0, len(h.attrs)+record.NumAttrs())
	attrs = append(attrs, h.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})

	if len(attrs) > 0 {
		fields := make([]string, 0, len(attrs))
		for _, attr := range attrs {
			key := attr.Key
			if h.group != "" {
				key = h.group + "." + key
			}
			fields = append(fields, fmt.Sprintf("%s=%v", colorize(colorBlue, key), attr.Value.Any()))
		}
		parts = append(parts, strings.Join(fields, " "))
	}

	_, err := fmt.Fprintln(h.writer, strings.Join(parts, " "))
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := *h
	cloned.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &cloned
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	cloned := *h
	if h.group == "" {
		cloned.group = name
	} else {
		cloned.group = h.group + "." + name
	}
	return &cloned
}

func GetEchoLogger(logger *slog.Logger) echo.MiddlewareFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:    true,
		LogMethod:    true,
		LogURI:       true,
		LogLatency:   true,
		LogUserAgent: true,
		Skipper: func(c *echo.Context) bool {
			return c.Request().URL.Path == "/health"
		},
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			reqLogger := c.Logger().With(
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.Duration("latency", v.Latency),
			)
			// if v.UserAgent != "" {
			// 	reqLogger = reqLogger.With(slog.String("ua", v.UserAgent))
			// }

			switch {
			case v.Status >= 500:
				reqLogger.Error("http_request")
			case v.Status >= 400:
				reqLogger.Warn("http_request")
			default:
				reqLogger.Info("http_request")
			}
			return nil
		},
	})
}

func padLevel(level string) string {
	return strings.ToUpper(fmt.Sprintf("%-5s", level))
}

func levelColor(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return colorGray
	case level < slog.LevelWarn:
		return colorGreen
	case level < slog.LevelError:
		return colorYellow
	default:
		return colorRed
	}
}

func colorize(color, text string) string {
	return color + text + colorReset
}

func Since(start time.Time) slog.Attr {
	return slog.Duration("latency", time.Since(start))
}
