package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"gateway/configs"
)

func New(output io.Writer, config configs.Logging) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToUpper(config.Level) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", config.Level)
	}
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})).With("application", config.ApplicationName), nil
}
