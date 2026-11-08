package routing

import (
	"path/filepath"
	"testing"

	"gateway/configs"
	"gateway/internal/api/balancer"
	"gateway/internal/api/plugin"
	"gateway/internal/api/plugin/ratelimit"
)

func TestCompileProjectRoutes(t *testing.T) {
	configPath := filepath.Join("..", "..", "..", "configs", "env.yaml")
	cfg, err := configs.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewRegistry()
	if err := plugins.Register(ratelimit.Factory{}); err != nil {
		t.Fatal(err)
	}
	routes, err := Compile(&cfg, balancer.NewRegistry(), plugins)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"/api/v1/items": "/api/v1/items", "/api/v1/items/:id": "/api/v1/items/{id}", "/delivery/v3/records": "/v3/records", "/delivery/v3/push/:push_type/:push_id": "/v3/push/{push_type}/{push_id}"}
	if len(routes) != len(want) {
		t.Fatalf("routes = %d, want %d", len(routes), len(want))
	}
	for _, route := range routes {
		if expected, exists := want[route.Route.ExternalPath]; !exists || expected != route.Route.UpstreamPattern {
			t.Errorf("route %s -> %s is unexpected", route.Route.ExternalPath, route.Route.UpstreamPattern)
		}
		if got := route.Route.PluginNames[len(route.Route.PluginNames)-1]; got != "gateway-balancer" {
			t.Errorf("last plugin = %q", got)
		}
	}
}

func TestValidateUpstreamParameters(t *testing.T) {
	if err := validateUpstreamParameters("/v1/items/{id}", "/internal/{missing}"); err == nil {
		t.Fatal("unknown upstream parameter was accepted")
	}
}

func TestBuildPluginsRequiresBalancer(t *testing.T) {
	_, _, err := buildPlugins(nil, nil, nil, plugin.NewRegistry(), nil, "gateway-balancer", "x-request-id")
	if err == nil {
		t.Fatal("buildPlugins() accepted a route without balancer")
	}
}
