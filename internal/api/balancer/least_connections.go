package balancer

import (
	"sync"

	"gateway/configs"
	"gateway/internal/api/domain"
)

type leastConnectionsFactory struct{}

func (leastConnectionsFactory) Strategy() string { return "least_connections" }
func (leastConnectionsFactory) Build(service domain.Service, _ configs.BalancerConfig) (Balancer, error) {
	return &leastConnections{instances: service.Instances, active: make(map[string]uint64)}, nil
}

type leastConnections struct {
	instances []domain.Instance
	mu        sync.Mutex
	active    map[string]uint64
	next      int
}

func (l *leastConnections) Pick(domain.RequestMeta) (domain.Instance, error) {
	if len(l.instances) == 0 {
		return domain.Instance{}, ErrNoInstances
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	minimum := ^uint64(0)
	for _, instance := range l.instances {
		if l.active[instance.ID] < minimum {
			minimum = l.active[instance.ID]
		}
	}
	for offset := 0; offset < len(l.instances); offset++ {
		index := (l.next + offset) % len(l.instances)
		instance := l.instances[index]
		if l.active[instance.ID] == minimum {
			l.active[instance.ID]++
			l.next = (index + 1) % len(l.instances)
			return instance, nil
		}
	}
	return domain.Instance{}, ErrNoInstances
}

func (l *leastConnections) Done(instanceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[instanceID] > 0 {
		l.active[instanceID]--
	}
}
