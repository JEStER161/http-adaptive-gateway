package configs

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	Mode        string      `yaml:"mode"`
	Logging     Logging     `yaml:"logging"`
	Listen      Listen      `yaml:"listen"`
	Proxy       Proxy       `yaml:"proxy"`
	Application Application `yaml:"application"`
	Routes      []Route     `yaml:"-"`
}

type Logging struct {
	Level              string `yaml:"level"`
	Encoding           string `yaml:"encoding"`
	RequestIDFieldName string `yaml:"requestIDFieldName"`
	ApplicationName    string `yaml:"applicationName"`
}

type Listen struct {
	HTTP HTTP `yaml:"http"`
}

type HTTP struct {
	IsEnabled         bool     `yaml:"isEnabled"`
	Host              string   `yaml:"host"`
	Port              int      `yaml:"port"`
	ReadHeaderTimeout Duration `yaml:"readHeaderTimeout"`
	ReadTimeout       Duration `yaml:"readTimeout"`
	WriteTimeout      Duration `yaml:"writeTimeout"`
	IdleTimeout       Duration `yaml:"idleTimeout"`
	ShutdownTimeout   Duration `yaml:"shutdownTimeout"`
}

func (h HTTP) Address() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

type Proxy struct {
	DialTimeout           Duration `yaml:"dialTimeout"`
	ResponseHeaderTimeout Duration `yaml:"responseHeaderTimeout"`
	IdleConnTimeout       Duration `yaml:"idleConnTimeout"`
	MaxIdleConns          int      `yaml:"maxIdleConns"`
	MaxIdleConnsPerHost   int      `yaml:"maxIdleConnsPerHost"`
}

type Application struct {
	RouteConfigPaths   []string       `yaml:"routeConfigPaths"`
	RoutePluginsGlobal []GlobalPlugin `yaml:"routePluginsGlobal"`
}

type GlobalPlugin struct {
	Name   string         `yaml:"name"`
	Alias  string         `yaml:"alias"`
	Params BalancerConfig `yaml:"params"`
}

type BalancerConfig struct {
	Strategy         string                 `yaml:"strategy"`
	Random           RandomConfig           `yaml:"random"`
	LeastConnections LeastConnectionsConfig `yaml:"leastConnections"`
	ConsistentHash   ConsistentHashConfig   `yaml:"consistentHash"`
	Adaptive         AdaptiveConfig         `yaml:"adaptive"`
}

type RandomConfig struct {
	Seed int64 `yaml:"seed"`
}

type LeastConnectionsConfig struct {
	TieBreaker string `yaml:"tieBreaker"`
}

type ConsistentHashConfig struct {
	VirtualNodes int           `yaml:"virtualNodes"`
	Key          HashKeyConfig `yaml:"key"`
}

type HashKeyConfig struct {
	Source   string `yaml:"source"`
	Name     string `yaml:"name"`
	Fallback string `yaml:"fallback"`
}

type AdaptiveConfig struct {
	ObservationWindow Duration         `yaml:"observationWindow"`
	MinSamples        int              `yaml:"minSamples"`
	ExplorationRate   float64          `yaml:"explorationRate"`
	Metrics           []AdaptiveMetric `yaml:"metrics"`
}

type AdaptiveMetric struct {
	Type   string    `yaml:"type"`
	Weight float64   `yaml:"weight"`
	Params yaml.Node `yaml:"params"`
}

type Route struct {
	Name                string  `yaml:"name"`
	Comment             string  `yaml:"comment"`
	Type                string  `yaml:"type"`
	Endpoint            string  `yaml:"endpoint"`
	Manifest            string  `yaml:"manifest"`
	IsValidationEnabled bool    `yaml:"is_validation_enabled"`
	Backend             Backend `yaml:"backend"`
	ManifestPath        string  `yaml:"-"`
}

type Backend struct {
	Hosts []string `yaml:"hosts"`
	SSL   SSL      `yaml:"ssl"`
}

type SSL struct {
	IsEnabled bool `yaml:"isEnabled"`
}

type routeFile struct {
	Routes     []Route             `yaml:"routes"`
	HostGroups map[string][]string `yaml:",inline"`
}

func Load(path string) (Config, error) {
	var cfg Config
	if err := decodeStrictFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("load main config: %w", err)
	}
	applyDefaults(&cfg)
	if err := cfg.validateMain(); err != nil {
		return Config{}, fmt.Errorf("validate main config: %w", err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve main config directory: %w", err)
	}
	for _, relative := range cfg.Application.RouteConfigPaths {
		routePath := resolvePath(base, relative)
		var file routeFile
		if err := decodeStrictFile(routePath, &file); err != nil {
			return Config{}, fmt.Errorf("load route config %q: %w", relative, err)
		}
		if err := validateHostGroupNames(file.HostGroups); err != nil {
			return Config{}, fmt.Errorf("validate route config %q: %w", relative, err)
		}
		for i := range file.Routes {
			file.Routes[i].ManifestPath = resolvePath(filepath.Dir(routePath), file.Routes[i].Manifest)
		}
		cfg.Routes = append(cfg.Routes, file.Routes...)
	}
	if err := cfg.validateRoutes(); err != nil {
		return Config{}, fmt.Errorf("validate routes: %w", err)
	}
	return cfg, nil
}

func validateHostGroupNames(groups map[string][]string) error {
	for name := range groups {
		if !strings.HasSuffix(name, "-hosts") {
			return fmt.Errorf("unknown field %q: host group names must end with -hosts", name)
		}
	}
	return nil
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return fmt.Errorf("decode trailing document: %w", err)
	}
	return nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

func applyDefaults(cfg *Config) {
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "INFO"
	}
	if cfg.Logging.Encoding == "" {
		cfg.Logging.Encoding = "json"
	}
	if cfg.Logging.RequestIDFieldName == "" {
		cfg.Logging.RequestIDFieldName = "x-request-id"
	}
	balancer := &cfg.Application.RoutePluginsGlobal
	if len(*balancer) == 1 {
		p := &(*balancer)[0].Params
		if p.LeastConnections.TieBreaker == "" {
			p.LeastConnections.TieBreaker = "round_robin"
		}
		if p.ConsistentHash.VirtualNodes == 0 {
			p.ConsistentHash.VirtualNodes = 128
		}
		if p.ConsistentHash.Key.Source == "" {
			p.ConsistentHash.Key.Source = "remote_ip"
		}
	}
}

func (c Config) BalancerPlugin() GlobalPlugin { return c.Application.RoutePluginsGlobal[0] }

func (c Config) validateMain() error {
	var problems []string
	if c.Mode != "dev" && c.Mode != "prod" {
		problems = append(problems, "mode must be dev or prod")
	}
	if c.Logging.Encoding != "json" {
		problems = append(problems, "logging.encoding must be json")
	}
	switch strings.ToUpper(c.Logging.Level) {
	case "DEBUG", "INFO", "WARN", "ERROR":
	default:
		problems = append(problems, "unsupported logging.level")
	}
	if strings.TrimSpace(c.Logging.ApplicationName) == "" {
		problems = append(problems, "logging.applicationName is required")
	}
	if !c.Listen.HTTP.IsEnabled {
		problems = append(problems, "listen.http.isEnabled must be true")
	}
	if c.Listen.HTTP.Port < 1 || c.Listen.HTTP.Port > 65535 {
		problems = append(problems, "listen.http.port must be between 1 and 65535")
	}
	for name, value := range map[string]time.Duration{
		"readHeaderTimeout": c.Listen.HTTP.ReadHeaderTimeout.Duration, "readTimeout": c.Listen.HTTP.ReadTimeout.Duration,
		"writeTimeout": c.Listen.HTTP.WriteTimeout.Duration, "idleTimeout": c.Listen.HTTP.IdleTimeout.Duration,
		"shutdownTimeout": c.Listen.HTTP.ShutdownTimeout.Duration, "dialTimeout": c.Proxy.DialTimeout.Duration,
		"responseHeaderTimeout": c.Proxy.ResponseHeaderTimeout.Duration, "idleConnTimeout": c.Proxy.IdleConnTimeout.Duration,
	} {
		if value <= 0 {
			problems = append(problems, name+" must be positive")
		}
	}
	if c.Proxy.MaxIdleConns <= 0 || c.Proxy.MaxIdleConnsPerHost <= 0 {
		problems = append(problems, "proxy idle connection limits must be positive")
	}
	if len(c.Application.RouteConfigPaths) == 0 {
		problems = append(problems, "application.routeConfigPaths is required")
	}
	if len(c.Application.RoutePluginsGlobal) != 1 {
		problems = append(problems, "exactly one global balancer plugin is required")
	} else {
		plugin := c.Application.RoutePluginsGlobal[0]
		if plugin.Name != "balancer" {
			problems = append(problems, "global plugin name must be balancer")
		}
		if strings.TrimSpace(plugin.Alias) == "" {
			problems = append(problems, "global balancer alias is required")
		}
		if err := validateBalancer(plugin.Params); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateBalancer(config BalancerConfig) error {
	var problems []string
	switch config.Strategy {
	case "round_robin", "random", "least_connections", "consistent_hash", "adaptive":
	default:
		problems = append(problems, "unknown balancing strategy "+config.Strategy)
	}
	if config.LeastConnections.TieBreaker != "round_robin" {
		problems = append(problems, "leastConnections.tieBreaker must be round_robin")
	}
	if config.ConsistentHash.VirtualNodes <= 0 {
		problems = append(problems, "consistentHash.virtualNodes must be positive")
	}
	if config.ConsistentHash.Key.Source != "remote_ip" && config.ConsistentHash.Key.Source != "header" {
		problems = append(problems, "consistentHash.key.source must be header or remote_ip")
	}
	if config.ConsistentHash.Key.Source == "header" && strings.TrimSpace(config.ConsistentHash.Key.Name) == "" {
		problems = append(problems, "consistentHash.key.name is required for header source")
	}
	if config.ConsistentHash.Key.Fallback != "" && config.ConsistentHash.Key.Fallback != "remote_ip" {
		problems = append(problems, "consistentHash.key.fallback must be remote_ip")
	}
	if config.Adaptive.ObservationWindow.Duration <= 0 || config.Adaptive.MinSamples <= 0 {
		problems = append(problems, "adaptive observationWindow and minSamples must be positive")
	}
	if config.Adaptive.ExplorationRate < 0 || config.Adaptive.ExplorationRate > 1 {
		problems = append(problems, "adaptive.explorationRate must be between 0 and 1")
	}
	weight := 0.0
	for _, metric := range config.Adaptive.Metrics {
		if metric.Weight <= 0 {
			problems = append(problems, "adaptive metric weight must be positive")
		}
		weight += metric.Weight
		if err := validateMetric(metric); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(config.Adaptive.Metrics) == 0 || weight < 0.999999 || weight > 1.000001 {
		problems = append(problems, "adaptive metric weights must sum to 1")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateMetric(metric AdaptiveMetric) error {
	switch metric.Type {
	case "latency_ewma":
		var p struct {
			Alpha  float64  `yaml:"alpha"`
			Target Duration `yaml:"target"`
		}
		if err := decodeStrictNode(&metric.Params, &p); err != nil {
			return fmt.Errorf("latency_ewma params: %w", err)
		}
		if p.Alpha <= 0 || p.Alpha > 1 || p.Target.Duration <= 0 {
			return errors.New("latency_ewma alpha must be in (0,1] and target positive")
		}
	case "error_rate":
		var p struct {
			Threshold float64 `yaml:"threshold"`
		}
		if err := decodeStrictNode(&metric.Params, &p); err != nil {
			return fmt.Errorf("error_rate params: %w", err)
		}
		if p.Threshold < 0 || p.Threshold > 1 {
			return errors.New("error_rate threshold must be between 0 and 1")
		}
	case "inflight":
		var p struct {
			SoftLimit int `yaml:"softLimit"`
		}
		if err := decodeStrictNode(&metric.Params, &p); err != nil {
			return fmt.Errorf("inflight params: %w", err)
		}
		if p.SoftLimit <= 0 {
			return errors.New("inflight softLimit must be positive")
		}
	default:
		return fmt.Errorf("unknown adaptive metric %q", metric.Type)
	}
	return nil
}

func decodeStrictNode(node *yaml.Node, target any) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func (c Config) validateRoutes() error {
	if len(c.Routes) == 0 {
		return errors.New("at least one route is required")
	}
	names, endpoints := map[string]struct{}{}, map[string]struct{}{}
	var problems []string
	for _, route := range c.Routes {
		if strings.TrimSpace(route.Name) == "" {
			problems = append(problems, "route name is required")
		}
		if _, exists := names[route.Name]; exists {
			problems = append(problems, "duplicate route name "+route.Name)
		}
		names[route.Name] = struct{}{}
		if route.Type != "openapi" {
			problems = append(problems, "route "+route.Name+" type must be openapi")
		}
		if route.IsValidationEnabled {
			problems = append(problems, "route "+route.Name+" is_validation_enabled must be false: OpenAPI runtime validation is not implemented")
		}
		if !strings.HasPrefix(route.Endpoint, "/") || route.Endpoint == "/" || strings.HasSuffix(route.Endpoint, "/") {
			problems = append(problems, "route "+route.Name+" endpoint must start with / and have no trailing /")
		}
		if _, exists := endpoints[route.Endpoint]; exists {
			problems = append(problems, "duplicate route endpoint "+route.Endpoint)
		}
		endpoints[route.Endpoint] = struct{}{}
		if len(route.Backend.Hosts) == 0 {
			problems = append(problems, "route "+route.Name+" backend requires hosts")
		}
		if route.Backend.SSL.IsEnabled {
			problems = append(problems, "route "+route.Name+" backend.ssl.isEnabled must be false: custom backend TLS settings are not implemented")
		}
		hostURLs := map[string]struct{}{}
		for _, host := range route.Backend.Hosts {
			if _, exists := hostURLs[host]; exists {
				problems = append(problems, "duplicate backend host "+host+" in route "+route.Name)
			}
			hostURLs[host] = struct{}{}
			parsed, err := url.Parse(host)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
				problems = append(problems, "invalid backend host URL "+host+" in route "+route.Name)
			}
		}
		if _, err := os.Stat(route.ManifestPath); err != nil {
			problems = append(problems, "manifest for route "+route.Name+": "+err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
