package http

import (
	"log/slog"
	"time"

	"gateway/internal/api/domain"

	"github.com/labstack/echo/v4"
)

func routeMetadata(route domain.Route) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(domain.ContextRouteID, route.ID)
			c.Set(domain.ContextServiceID, route.Service.ID)
			c.Set(domain.ContextPlugins, route.PluginNames)
			return next(c)
		}
	}
}

func requestLogging(logger *slog.Logger, requestIDHeader string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			started := time.Now()
			err := next(c)
			hadError := err != nil
			if err != nil {
				c.Error(err)
			}
			attributes := []any{
				"request_id", c.Response().Header().Get(requestIDHeader),
				"method", c.Request().Method, "path", c.Request().URL.Path,
				"route_id", value(c, domain.ContextRouteID), "service_id", value(c, domain.ContextServiceID),
				"instance_id", value(c, domain.ContextInstanceID), "plugins", value(c, domain.ContextPlugins),
				"status", c.Response().Status, "duration", time.Since(started),
			}
			if hadError {
				attributes = append(attributes, "error_type", "request_error")
			}
			logger.Info("request completed", attributes...)
			return nil
		}
	}
}

func value(c echo.Context, key string) any {
	if result := c.Get(key); result != nil {
		return result
	}
	return ""
}
