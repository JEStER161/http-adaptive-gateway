package balancer

import (
	"sync/atomic"

	"gateway/configs"
	"gateway/internal/api/domain"
)

type roundRobinFactory struct{}

func (roundRobinFactory) Strategy() string { return "round_robin" }
func (roundRobinFactory) Build(service domain.Service, _ configs.BalancerConfig) (Balancer, error) {
	return &roundRobin{instances: service.Instances}, nil
}

type roundRobin struct {
	instances []domain.Instance
	next      atomic.Uint64
}

func (r *roundRobin) Pick(domain.RequestMeta) (domain.Instance, error) {
	if len(r.instances) == 0 {
		return domain.Instance{}, ErrNoInstances
	}
	index := (r.next.Add(1) - 1) % uint64(len(r.instances))
	return r.instances[index], nil
}
func (*roundRobin) Done(string) {}
