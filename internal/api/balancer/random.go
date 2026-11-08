package balancer

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"sync"

	"gateway/configs"
	"gateway/internal/api/domain"
)

type randomFactory struct{}

func (randomFactory) Strategy() string { return "random" }
func (randomFactory) Build(service domain.Service, config configs.BalancerConfig) (Balancer, error) {
	seed := serviceSeed(config.Random.Seed, service.ID)
	if seed == 0 {
		var raw [8]byte
		if _, err := cryptorand.Read(raw[:]); err != nil {
			return nil, err
		}
		seed = int64(binary.LittleEndian.Uint64(raw[:]))
	}
	return &randomBalancer{instances: service.Instances, random: rand.New(rand.NewSource(seed))}, nil
}

type randomBalancer struct {
	instances []domain.Instance
	mu        sync.Mutex
	random    *rand.Rand
}

func (r *randomBalancer) Pick(domain.RequestMeta) (domain.Instance, error) {
	if len(r.instances) == 0 {
		return domain.Instance{}, ErrNoInstances
	}
	r.mu.Lock()
	index := r.random.Intn(len(r.instances))
	r.mu.Unlock()
	return r.instances[index], nil
}
func (*randomBalancer) Done(string) {}
