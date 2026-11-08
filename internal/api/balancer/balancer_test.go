package balancer

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"gateway/configs"
	"gateway/internal/api/domain"
)

func testService(id string) domain.Service {
	return domain.Service{ID: id, Instances: []domain.Instance{{ID: "one", URL: &url.URL{}}, {ID: "two", URL: &url.URL{}}, {ID: "three", URL: &url.URL{}}}}
}

func TestRoundRobinSequenceAndIndependentServices(t *testing.T) {
	registry := NewRegistry()
	config := configs.BalancerConfig{Strategy: "round_robin"}
	first, err := registry.Build(testService("first"), config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Build(testService("second"), config)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "three", "one"}
	for index, expected := range want {
		got, err := first.Pick(domain.RequestMeta{})
		if err != nil || got.ID != expected {
			t.Fatalf("pick %d = %q, %v; want %q", index, got.ID, err, expected)
		}
	}
	got, _ := second.Pick(domain.RequestMeta{})
	if got.ID != "one" {
		t.Fatalf("second service first pick = %q", got.ID)
	}
}

func TestRoundRobinConcurrent(t *testing.T) {
	value, _ := NewRegistry().Build(testService("service"), configs.BalancerConfig{Strategy: "round_robin"})
	counts := make(map[string]int)
	var mutex sync.Mutex
	var group sync.WaitGroup
	for range 300 {
		group.Add(1)
		go func() {
			defer group.Done()
			instance, err := value.Pick(domain.RequestMeta{})
			if err != nil {
				t.Error(err)
				return
			}
			mutex.Lock()
			counts[instance.ID]++
			mutex.Unlock()
		}()
	}
	group.Wait()
	for _, id := range []string{"one", "two", "three"} {
		if counts[id] != 100 {
			t.Fatalf("count[%s] = %d", id, counts[id])
		}
	}
}

func TestLeastConnectionsTracksCompletion(t *testing.T) {
	value, _ := NewRegistry().Build(testService("service"), configs.BalancerConfig{Strategy: "least_connections"})
	first, _ := value.Pick(domain.RequestMeta{})
	second, _ := value.Pick(domain.RequestMeta{})
	third, _ := value.Pick(domain.RequestMeta{})
	if first.ID != "one" || second.ID != "two" || third.ID != "three" {
		t.Fatalf("unexpected picks: %s %s %s", first.ID, second.ID, third.ID)
	}
	value.Done(first.ID)
	next, _ := value.Pick(domain.RequestMeta{})
	if next.ID != "one" {
		t.Fatalf("pick after completion = %s", next.ID)
	}
}

func TestConsistentHashIsStable(t *testing.T) {
	config := configs.BalancerConfig{Strategy: "consistent_hash", ConsistentHash: configs.ConsistentHashConfig{VirtualNodes: 64, Key: configs.HashKeyConfig{Source: "header", Name: "X-Key", Fallback: "remote_ip"}}}
	value, err := NewRegistry().Build(testService("service"), config)
	if err != nil {
		t.Fatal(err)
	}
	meta := domain.RequestMeta{Headers: http.Header{"X-Key": []string{"customer-1"}}, RemoteIP: "127.0.0.1"}
	first, _ := value.Pick(meta)
	for range 20 {
		got, _ := value.Pick(meta)
		if got.ID != first.ID {
			t.Fatalf("hash changed from %s to %s", first.ID, got.ID)
		}
	}
}

func TestAdaptiveIsReserved(t *testing.T) {
	_, err := NewRegistry().Build(testService("service"), configs.BalancerConfig{Strategy: "adaptive"})
	if err == nil {
		t.Fatal("adaptive strategy unexpectedly built")
	}
}

func TestNoInstances(t *testing.T) {
	value, err := NewRegistry().Build(domain.Service{ID: "empty"}, configs.BalancerConfig{Strategy: "round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.Pick(domain.RequestMeta{}); err != ErrNoInstances {
		t.Fatalf("Pick() error = %v, want ErrNoInstances", err)
	}
}

func TestRandomSeedIsReproducible(t *testing.T) {
	config := configs.BalancerConfig{Strategy: "random", Random: configs.RandomConfig{Seed: 42}}
	first, _ := NewRegistry().Build(testService("service"), config)
	second, _ := NewRegistry().Build(testService("service"), config)
	for range 20 {
		left, _ := first.Pick(domain.RequestMeta{})
		right, _ := second.Pick(domain.RequestMeta{})
		if left.ID != right.ID {
			t.Fatalf("seeded sequences differ: %s != %s", left.ID, right.ID)
		}
	}
}
