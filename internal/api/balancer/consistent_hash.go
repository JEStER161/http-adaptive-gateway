package balancer

import (
	"fmt"
	"hash/crc32"
	"sort"

	"gateway/configs"
	"gateway/internal/api/domain"
)

type consistentHashFactory struct{}

func (consistentHashFactory) Strategy() string { return "consistent_hash" }
func (consistentHashFactory) Build(service domain.Service, config configs.BalancerConfig) (Balancer, error) {
	balancer := &consistentHash{instances: make(map[uint32]domain.Instance), key: config.ConsistentHash.Key}
	for _, instance := range service.Instances {
		for virtual := 0; virtual < config.ConsistentHash.VirtualNodes; virtual++ {
			point := crc32.ChecksumIEEE([]byte(fmt.Sprintf("%s#%d", instance.ID, virtual)))
			balancer.points = append(balancer.points, point)
			balancer.instances[point] = instance
		}
	}
	sort.Slice(balancer.points, func(i, j int) bool { return balancer.points[i] < balancer.points[j] })
	return balancer, nil
}

type consistentHash struct {
	points    []uint32
	instances map[uint32]domain.Instance
	key       configs.HashKeyConfig
}

func (c *consistentHash) Pick(meta domain.RequestMeta) (domain.Instance, error) {
	if len(c.points) == 0 {
		return domain.Instance{}, ErrNoInstances
	}
	key := ""
	if c.key.Source == "header" {
		key = meta.Headers.Get(c.key.Name)
	} else {
		key = meta.RemoteIP
	}
	if key == "" && c.key.Fallback == "remote_ip" {
		key = meta.RemoteIP
	}
	if key == "" {
		return domain.Instance{}, ErrRoutingKey
	}
	hash := crc32.ChecksumIEEE([]byte(key))
	index := sort.Search(len(c.points), func(i int) bool { return c.points[i] >= hash })
	if index == len(c.points) {
		index = 0
	}
	return c.instances[c.points[index]], nil
}
func (*consistentHash) Done(string) {}
