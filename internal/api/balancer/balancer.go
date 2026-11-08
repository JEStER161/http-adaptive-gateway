package balancer

import (
	"errors"
	"fmt"
	"hash/fnv"

	"gateway/configs"
	"gateway/internal/api/domain"
)

var (
	ErrNoInstances = errors.New("no available instances")
	ErrRoutingKey  = errors.New("routing key is unavailable")
)

type Balancer interface {
	Pick(domain.RequestMeta) (domain.Instance, error)
	Done(instanceID string)
}

type Factory interface {
	Strategy() string
	Build(domain.Service, configs.BalancerConfig) (Balancer, error)
}

type Registry struct{ factories map[string]Factory }

func NewRegistry() *Registry {
	r := &Registry{factories: make(map[string]Factory)}
	for _, factory := range []Factory{roundRobinFactory{}, randomFactory{}, leastConnectionsFactory{}, consistentHashFactory{}} {
		r.factories[factory.Strategy()] = factory
	}
	return r
}

func (r *Registry) Build(service domain.Service, config configs.BalancerConfig) (Balancer, error) {
	if config.Strategy == "adaptive" {
		return nil, errors.New("adaptive strategy is not implemented in this version")
	}
	factory, ok := r.factories[config.Strategy]
	if !ok {
		return nil, fmt.Errorf("unknown balancing strategy %q", config.Strategy)
	}
	return factory.Build(service, config)
}

func serviceSeed(base int64, serviceID string) int64 {
	if base == 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(serviceID))
	return base ^ int64(h.Sum64())
}
