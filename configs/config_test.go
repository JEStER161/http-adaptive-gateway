package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectConfig(t *testing.T) {
	cfg, err := Load("env.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.BalancerPlugin().Params.Strategy, "round_robin"; got != want {
		t.Fatalf("strategy = %q, want %q", got, want)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(cfg.Services))
	}
	if !filepath.IsAbs(cfg.Services[0].ManifestPath) {
		t.Fatalf("manifest path was not resolved: %q", cfg.Services[0].ManifestPath)
	}
}

func TestLoadRejectsUnknownMainField(t *testing.T) {
	data, err := os.ReadFile("env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "env.yaml")
	if err := os.WriteFile(path, append(data, []byte("\nunknownField: true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unknownField not found") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestServiceFileRejectsBalancingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.yaml")
	data := []byte("services:\n  - id: api\n    prefix: /api\n    manifest: manifest.yaml\n    hosts: []\n    strategy: random\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var file serviceFile
	err := decodeStrictFile(path, &file)
	if err == nil || !strings.Contains(err.Error(), "field strategy not found") {
		t.Fatalf("decodeStrictFile() error = %v, want unknown strategy field", err)
	}
}

func TestValidateBalancerRejectsInvalidAdaptiveMetric(t *testing.T) {
	cfg, err := Load("env.yaml")
	if err != nil {
		t.Fatal(err)
	}
	params := cfg.BalancerPlugin().Params
	params.Adaptive.Metrics[0].Params.Content[1].Value = "2"
	if err := validateBalancer(params); err == nil {
		t.Fatal("validateBalancer() succeeded for alpha > 1")
	}
}
