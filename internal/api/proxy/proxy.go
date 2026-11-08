package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"gateway/configs"
	"gateway/delivery/http/model"
	"gateway/internal/api/balancer"
	"gateway/internal/api/domain"
	"gateway/internal/api/observer"

	"github.com/labstack/echo/v4"
)

type contextKey struct{}

var templateParameter = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type requestState struct {
	serviceID     string
	instance      domain.Instance
	path, rawPath string
	requestID     string
	started       time.Time
	context       context.Context
	status        int
	err           error
	once          sync.Once
	balancer      balancer.Balancer
	observer      observer.Observer
}

func (s *requestState) finish() {
	s.once.Do(func() {
		if s.err == nil && s.context.Err() != nil {
			s.err = s.context.Err()
		}
		s.balancer.Done(s.instance.ID)
		s.observer.RequestFinished(s.serviceID, s.instance.ID, domain.UpstreamResult{Duration: time.Since(s.started), StatusCode: s.status, Err: s.err})
	})
}

type Proxy struct {
	reverse         *httputil.ReverseProxy
	observer        observer.Observer
	requestIDHeader string
	logger          *slog.Logger
}

func (p *Proxy) CloseIdleConnections() {
	if closer, ok := p.reverse.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func New(config configs.Proxy, requestIDHeader string, upstreamObserver observer.Observer, logger *slog.Logger) *Proxy {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: config.DialTimeout.Duration, KeepAlive: 30 * time.Second}).DialContext, ResponseHeaderTimeout: config.ResponseHeaderTimeout.Duration, IdleConnTimeout: config.IdleConnTimeout.Duration, MaxIdleConns: config.MaxIdleConns, MaxIdleConnsPerHost: config.MaxIdleConnsPerHost, ForceAttemptHTTP2: true}
	return NewWithTransport(requestIDHeader, upstreamObserver, transport, logger)
}

func NewWithTransport(requestIDHeader string, upstreamObserver observer.Observer, transport http.RoundTripper, logger *slog.Logger) *Proxy {
	value := &Proxy{observer: upstreamObserver, requestIDHeader: requestIDHeader, logger: logger}
	value.reverse = &httputil.ReverseProxy{Transport: transport, Rewrite: value.rewrite, ModifyResponse: value.modifyResponse, ErrorHandler: value.errorHandler}
	return value
}

func (p *Proxy) Handler(route domain.Route, routeBalancer balancer.Balancer) echo.HandlerFunc {
	return func(c echo.Context) error {
		instance, ok := c.Get(domain.ContextUpstream).(domain.Instance)
		if !ok {
			return errors.New("balancer did not select an upstream")
		}
		path, rawPath := renderPath(route.UpstreamPattern, c)
		state := &requestState{serviceID: route.Service.ID, instance: instance, path: path, rawPath: rawPath, requestID: c.Response().Header().Get(p.requestIDHeader), started: time.Now(), context: c.Request().Context(), balancer: routeBalancer, observer: p.observer}
		p.observer.RequestStarted(route.Service.ID, instance.ID)
		defer state.finish()
		// The upstream receives and returns the Gateway request ID. Remove the
		// middleware's provisional response header to avoid a duplicate value.
		c.Response().Header().Del(p.requestIDHeader)
		request := c.Request().Clone(context.WithValue(c.Request().Context(), contextKey{}, state))
		p.reverse.ServeHTTP(c.Response(), request)
		return nil
	}
}

func (p *Proxy) rewrite(proxyRequest *httputil.ProxyRequest) {
	state := proxyRequest.In.Context().Value(contextKey{}).(*requestState)
	proxyRequest.Out.URL.Scheme = state.instance.URL.Scheme
	proxyRequest.Out.URL.Host = state.instance.URL.Host
	proxyRequest.Out.URL.Path = state.path
	proxyRequest.Out.URL.RawPath = state.rawPath
	proxyRequest.Out.Host = state.instance.URL.Host
	proxyRequest.Out.Header.Set(p.requestIDHeader, state.requestID)
	proxyRequest.SetXForwarded()
}

func (p *Proxy) modifyResponse(response *http.Response) error {
	state := response.Request.Context().Value(contextKey{}).(*requestState)
	state.status = response.StatusCode
	response.Header.Set("X-Gateway-Upstream", state.instance.ID)
	response.Header.Set(p.requestIDHeader, state.requestID)
	if backendID := response.Header.Get("X-Instance-ID"); backendID != "" && backendID != state.instance.ID && p.logger != nil {
		p.logger.Warn("backend instance header does not match selected instance", "service_id", state.serviceID, "selected_instance_id", state.instance.ID, "backend_instance_id", backendID)
	}
	return nil
}

func (p *Proxy) errorHandler(writer http.ResponseWriter, request *http.Request, err error) {
	state := request.Context().Value(contextKey{}).(*requestState)
	state.err = err
	state.status = http.StatusBadGateway
	code, message := "upstream_unavailable", "backend service is temporarily unavailable"
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		state.status, code, message = http.StatusGatewayTimeout, "upstream_timeout", "backend service timed out"
	}
	writer.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	writer.Header().Set("X-Gateway-Upstream", state.instance.ID)
	writer.Header().Set(p.requestIDHeader, state.requestID)
	writer.WriteHeader(state.status)
	_ = json.NewEncoder(writer).Encode(model.ErrorResponse{Error: model.Error{Code: code, Message: message, RequestID: state.requestID}})
}

func renderPath(pattern string, c echo.Context) (string, string) {
	path := templateParameter.ReplaceAllStringFunc(pattern, func(token string) string { return c.Param(strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")) })
	raw := templateParameter.ReplaceAllStringFunc(pattern, func(token string) string {
		return url.PathEscape(c.Param(strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")))
	})
	if path == raw {
		raw = ""
	}
	return path, raw
}
