package observer

import (
	"log/slog"

	"gateway/internal/api/domain"
)

type Observer interface {
	RequestStarted(serviceID, instanceID string)
	RequestFinished(serviceID, instanceID string, result domain.UpstreamResult)
}

type Logging struct{ logger *slog.Logger }

func NewLogging(logger *slog.Logger) *Logging { return &Logging{logger: logger} }
func (l *Logging) RequestStarted(serviceID, instanceID string) {
	l.logger.Debug("upstream request started", "service_id", serviceID, "instance_id", instanceID)
}
func (l *Logging) RequestFinished(serviceID, instanceID string, result domain.UpstreamResult) {
	attributes := []any{"service_id", serviceID, "instance_id", instanceID, "duration", result.Duration, "status", result.StatusCode}
	if result.Err != nil {
		attributes = append(attributes, "error_type", "upstream_error")
	}
	l.logger.Info("upstream request finished", attributes...)
}
