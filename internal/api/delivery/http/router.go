package http

import (
	stderrors "errors"
	"log/slog"
	"net/http"

	"gateway/configs"
	"gateway/delivery/http/model"
	"gateway/internal/api/proxy"
	"gateway/internal/api/routing"
	apierrors "gateway/pkg/api/errors"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Router struct{ echo *echo.Echo }

func NewRouter(logger *slog.Logger, config *configs.Config, routes []routing.CompiledRoute, reverseProxy *proxy.Proxy) *Router {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.HTTPErrorHandler = errorHandler(config.Logging.RequestIDFieldName)
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{DisablePrintStack: true}))
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		TargetHeader: config.Logging.RequestIDFieldName,
		Generator:    uuid.NewString,
	}))
	e.Use(requestLogging(logger, config.Logging.RequestIDFieldName))
	e.GET("/health", func(c echo.Context) error { return c.JSON(http.StatusOK, map[string]string{"status": "ok"}) })
	for _, compiled := range routes {
		middlewares := make([]echo.MiddlewareFunc, 0, len(compiled.Middlewares)+1)
		middlewares = append(middlewares, routeMetadata(compiled.Route))
		middlewares = append(middlewares, compiled.Middlewares...)
		e.Add(compiled.Route.Method, compiled.Route.ExternalPath, reverseProxy.Handler(compiled.Route, compiled.Balancer), middlewares...)
	}
	return &Router{echo: e}
}

func (r *Router) Handler() http.Handler { return r.echo }

func errorHandler(requestIDHeader string) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		status, code, message := http.StatusInternalServerError, "internal_error", "internal server error"
		var applicationError *apierrors.Error
		if stderrors.As(err, &applicationError) {
			status, code, message = applicationError.Status, applicationError.Code, applicationError.Message
		} else {
			var echoError *echo.HTTPError
			if stderrors.As(err, &echoError) {
				status = echoError.Code
				switch status {
				case http.StatusNotFound:
					code, message = "route_not_found", "route not found"
				case http.StatusMethodNotAllowed:
					code, message = "method_not_allowed", "method not allowed"
				case http.StatusBadRequest:
					code, message = "bad_request", "bad request"
				}
			}
		}
		response := model.ErrorResponse{Error: model.Error{Code: code, Message: message, RequestID: c.Response().Header().Get(requestIDHeader)}}
		if writeErr := c.JSON(status, response); writeErr != nil {
			c.Echo().Logger.Error(writeErr)
		}
	}
}
