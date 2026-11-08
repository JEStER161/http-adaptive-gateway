package http

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gateway/configs"
	"gateway/internal/api/balancer"
	"gateway/internal/api/domain"
	"gateway/internal/api/plugin"
	"gateway/internal/api/plugin/ratelimit"
	"gateway/internal/api/proxy"
	"gateway/internal/api/routing"
)

type recordingObserver struct {
	mu                sync.Mutex
	started, finished int
	last              domain.UpstreamResult
}

type roundTripFunc func(*stdhttp.Request) (*stdhttp.Response, error)

func (f roundTripFunc) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return f(request)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "upstream timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func (o *recordingObserver) RequestStarted(string, string) { o.mu.Lock(); o.started++; o.mu.Unlock() }
func (o *recordingObserver) RequestFinished(_ string, _ string, result domain.UpstreamResult) {
	o.mu.Lock()
	o.finished++
	o.last = result
	o.mu.Unlock()
}

func TestGatewayRewritesPathAndPreservesRequest(t *testing.T) {
	var upstreamPath, upstreamQuery, upstreamBody, upstreamRequestID string
	transport := roundTripFunc(func(r *stdhttp.Request) (*stdhttp.Response, error) {
		upstreamPath, upstreamQuery = r.URL.Path, r.URL.RawQuery
		upstreamRequestID = r.Header.Get("x-request-id")
		body, _ := io.ReadAll(r.Body)
		upstreamBody = string(body)
		return &stdhttp.Response{StatusCode: stdhttp.StatusCreated, Header: stdhttp.Header{"X-Instance-ID": []string{"backend-1"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
	})

	cfg, err := configs.Load("../../../../configs/env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for routeIndex := range cfg.Routes {
		for hostIndex := range cfg.Routes[routeIndex].Backend.Hosts {
			cfg.Routes[routeIndex].Backend.Hosts[hostIndex] = "http://upstream.test"
		}
	}
	pluginRegistry := plugin.NewRegistry()
	if err := pluginRegistry.Register(ratelimit.Factory{}); err != nil {
		t.Fatal(err)
	}
	routes, err := routing.Compile(&cfg, balancer.NewRegistry(), pluginRegistry)
	if err != nil {
		t.Fatal(err)
	}
	observed := &recordingObserver{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reverse := proxy.NewWithTransport(cfg.Logging.RequestIDFieldName, observed, transport, logger)
	router := NewRouter(logger, &cfg, routes, reverse)
	request := httptest.NewRequest(stdhttp.MethodPost, "http://gateway.test/api/v1/items?source=test", bytes.NewBufferString(`{"name":"n","value":"v"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.Handler().ServeHTTP(recorder, request)
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d", recorder.Code)
	}
	if upstreamPath != "/api/v1/items" || upstreamQuery != "source=test" || upstreamBody != `{"name":"n","value":"v"}` {
		t.Fatalf("upstream got path=%q query=%q body=%q", upstreamPath, upstreamQuery, upstreamBody)
	}
	if recorder.Header().Get("X-Gateway-Upstream") != "api-1" {
		t.Fatalf("upstream header = %q", recorder.Header().Get("X-Gateway-Upstream"))
	}
	if upstreamRequestID == "" || len(recorder.Header().Values("x-request-id")) != 1 || recorder.Header().Get("x-request-id") != upstreamRequestID {
		t.Fatalf("request id was not propagated exactly once: upstream=%q response=%v", upstreamRequestID, recorder.Header().Values("x-request-id"))
	}
	if observed.started != 1 || observed.finished != 1 {
		t.Fatalf("observer = %d/%d", observed.started, observed.finished)
	}
}

func TestHealthAndNotFound(t *testing.T) {
	cfg, err := configs.Load("../../../../configs/env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pluginRegistry := plugin.NewRegistry()
	_ = pluginRegistry.Register(ratelimit.Factory{})
	routes, err := routing.Compile(&cfg, balancer.NewRegistry(), pluginRegistry)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(logger, &cfg, routes, proxy.New(cfg.Proxy, cfg.Logging.RequestIDFieldName, &recordingObserver{}, logger))
	health := httptest.NewRecorder()
	router.Handler().ServeHTTP(health, httptest.NewRequest(stdhttp.MethodGet, "/health", nil))
	if health.Code != 200 {
		t.Fatalf("health status = %d", health.Code)
	}
	missing := httptest.NewRecorder()
	router.Handler().ServeHTTP(missing, httptest.NewRequest(stdhttp.MethodGet, "/missing", nil))
	if missing.Code != 404 {
		t.Fatalf("missing status = %d", missing.Code)
	}
	wrongMethod := httptest.NewRecorder()
	router.Handler().ServeHTTP(wrongMethod, httptest.NewRequest(stdhttp.MethodDelete, "/api/v1/items", nil))
	if wrongMethod.Code != 405 {
		t.Fatalf("wrong method status = %d", wrongMethod.Code)
	}
}

func TestRateLimitStopsBeforeUpstream(t *testing.T) {
	cfg, err := configs.Load("../../../../configs/env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for routeIndex := range cfg.Routes {
		for hostIndex := range cfg.Routes[routeIndex].Backend.Hosts {
			cfg.Routes[routeIndex].Backend.Hosts[hostIndex] = "http://upstream.test"
		}
	}
	pluginRegistry := plugin.NewRegistry()
	_ = pluginRegistry.Register(ratelimit.Factory{})
	routes, err := routing.Compile(&cfg, balancer.NewRegistry(), pluginRegistry)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCalls := 0
	transport := roundTripFunc(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		upstreamCalls++
		return &stdhttp.Response{StatusCode: 200, Header: make(stdhttp.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(logger, &cfg, routes, proxy.NewWithTransport(cfg.Logging.RequestIDFieldName, &recordingObserver{}, transport, logger))
	lastStatus := 0
	for range 21 {
		recorder := httptest.NewRecorder()
		router.Handler().ServeHTTP(recorder, httptest.NewRequest(stdhttp.MethodGet, "/delivery/v3/records", nil))
		lastStatus = recorder.Code
	}
	if lastStatus != stdhttp.StatusTooManyRequests {
		t.Fatalf("last status = %d, want 429", lastStatus)
	}
	if upstreamCalls != 20 {
		t.Fatalf("upstream calls = %d, want 20", upstreamCalls)
	}
}

func TestUpstreamTimeoutUsesGatewayError(t *testing.T) {
	cfg, err := configs.Load("../../../../configs/env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for routeIndex := range cfg.Routes {
		for hostIndex := range cfg.Routes[routeIndex].Backend.Hosts {
			cfg.Routes[routeIndex].Backend.Hosts[hostIndex] = "http://upstream.test"
		}
	}
	pluginRegistry := plugin.NewRegistry()
	_ = pluginRegistry.Register(ratelimit.Factory{})
	routes, err := routing.Compile(&cfg, balancer.NewRegistry(), pluginRegistry)
	if err != nil {
		t.Fatal(err)
	}
	observed := &recordingObserver{}
	transport := roundTripFunc(func(*stdhttp.Request) (*stdhttp.Response, error) { return nil, timeoutError{} })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(logger, &cfg, routes, proxy.NewWithTransport(cfg.Logging.RequestIDFieldName, observed, transport, logger))
	recorder := httptest.NewRecorder()
	router.Handler().ServeHTTP(recorder, httptest.NewRequest(stdhttp.MethodPost, "/api/v1/items", bytes.NewBufferString(`{}`)))
	if recorder.Code != stdhttp.StatusGatewayTimeout || !bytes.Contains(recorder.Body.Bytes(), []byte("upstream_timeout")) {
		t.Fatalf("timeout response = %d %s", recorder.Code, recorder.Body.String())
	}
	if observed.started != 1 || observed.finished != 1 {
		t.Fatalf("observer = %d/%d", observed.started, observed.finished)
	}
}

func TestClientCancellationFinishesObservation(t *testing.T) {
	cfg, err := configs.Load("../../../../configs/env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for routeIndex := range cfg.Routes {
		for hostIndex := range cfg.Routes[routeIndex].Backend.Hosts {
			cfg.Routes[routeIndex].Backend.Hosts[hostIndex] = "http://upstream.test"
		}
	}
	pluginRegistry := plugin.NewRegistry()
	_ = pluginRegistry.Register(ratelimit.Factory{})
	routes, err := routing.Compile(&cfg, balancer.NewRegistry(), pluginRegistry)
	if err != nil {
		t.Fatal(err)
	}
	observed := &recordingObserver{}
	transport := roundTripFunc(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		return nil, request.Context().Err()
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(logger, &cfg, routes, proxy.NewWithTransport(cfg.Logging.RequestIDFieldName, observed, transport, logger))
	request := httptest.NewRequest(stdhttp.MethodPost, "/api/v1/items", bytes.NewBufferString(`{}`))
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	recorder := httptest.NewRecorder()
	router.Handler().ServeHTTP(recorder, request.WithContext(ctx))
	if observed.started != 1 || observed.finished != 1 || observed.last.Err == nil {
		t.Fatalf("cancellation observation = started:%d finished:%d error:%v", observed.started, observed.finished, observed.last.Err)
	}
}
