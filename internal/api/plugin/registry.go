package plugin

import (
	"fmt"
	"net/http"

	"gateway/internal/api/balancer"
	"gateway/internal/api/domain"
	apierrors "gateway/pkg/api/errors"

	"github.com/labstack/echo/v4"
	"gopkg.in/yaml.v3"
)

type Factory interface {
	Type() string
	Build(name string, config yaml.Node) (echo.MiddlewareFunc, error)
}

type Registry struct{ factories map[string]Factory }

func NewRegistry() *Registry { return &Registry{factories: make(map[string]Factory)} }
func (r *Registry) Register(factory Factory) error {
	if _, exists := r.factories[factory.Type()]; exists {
		return fmt.Errorf("plugin factory %q already registered", factory.Type())
	}
	r.factories[factory.Type()] = factory
	return nil
}
func (r *Registry) Build(pluginType, name string, config yaml.Node) (echo.MiddlewareFunc, error) {
	factory, exists := r.factories[pluginType]
	if !exists {
		return nil, fmt.Errorf("unknown plugin type %q", pluginType)
	}
	return factory.Build(name, config)
}

func BalancerMiddleware(value balancer.Balancer, requestIDHeader string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			instance, err := value.Pick(domain.RequestMeta{RequestID: c.Response().Header().Get(requestIDHeader), RemoteIP: c.RealIP(), Headers: c.Request().Header})
			if err != nil {
				if err == balancer.ErrRoutingKey {
					return apierrors.New(http.StatusBadRequest, "routing_key_unavailable", "routing key is unavailable", err)
				}
				return apierrors.New(http.StatusServiceUnavailable, "no_available_instances", "no backend instances are available", err)
			}
			c.Set(domain.ContextUpstream, instance)
			c.Set(domain.ContextInstanceID, instance.ID)
			return next(c)
		}
	}
}
