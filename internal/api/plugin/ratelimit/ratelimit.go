package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "gateway/pkg/api/errors"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

type Factory struct{}

func (Factory) Type() string { return "rate_limit" }

type config struct {
	RequestsPerSecond float64 `yaml:"requestsPerSecond"`
	Burst             int     `yaml:"burst"`
	Key               string  `yaml:"key"`
}

func (Factory) Build(name string, raw yaml.Node) (echo.MiddlewareFunc, error) {
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}
	if cfg.RequestsPerSecond <= 0 || cfg.Burst <= 0 {
		return nil, errors.New("rate_limit requestsPerSecond and burst must be positive")
	}
	if cfg.Key != "remote_ip" {
		return nil, errors.New("rate_limit key must be remote_ip")
	}
	limiters := &store{values: make(map[string]*rate.Limiter), limit: rate.Limit(cfg.RequestsPerSecond), burst: cfg.Burst}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !limiters.get(c.RealIP()).Allow() {
				retry := time.Duration(float64(time.Second) / cfg.RequestsPerSecond)
				seconds := int(retry.Round(time.Second) / time.Second)
				if seconds < 1 {
					seconds = 1
				}
				c.Response().Header().Set("Retry-After", strconv.Itoa(seconds))
				return apierrors.New(http.StatusTooManyRequests, "rate_limit_exceeded", "request rate limit exceeded", nil)
			}
			return next(c)
		}
	}, nil
}

type store struct {
	mu     sync.Mutex
	values map[string]*rate.Limiter
	limit  rate.Limit
	burst  int
}

func (s *store) get(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.values[key]
	if value == nil {
		value = rate.NewLimiter(s.limit, s.burst)
		s.values[key] = value
	}
	return value
}
