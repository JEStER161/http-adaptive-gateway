package routing

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gateway/configs"
	"gateway/internal/api/balancer"
	"gateway/internal/api/domain"
	"gateway/internal/api/plugin"

	"github.com/labstack/echo/v4"
	"gopkg.in/yaml.v3"
)

var pathParameterPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type CompiledRoute struct {
	Route       domain.Route
	Balancer    balancer.Balancer
	Middlewares []echo.MiddlewareFunc
}

type localPlugin struct {
	Type   string
	Params yaml.Node
}
type pluginUse struct {
	Ref              string
	Name             string
	InheritGlobal    bool
	InheritFromAlias string
}
type manifestRoute struct {
	ID, Method, Path, Upstream string
	Plugins                    []pluginUse
}
type manifest struct {
	Routes  []manifestRoute
	Plugins map[string]localPlugin
}

func Compile(config *configs.Config, balancers *balancer.Registry, plugins *plugin.Registry) ([]CompiledRoute, error) {
	global := config.BalancerPlugin()
	seenIDs, seenRoutes := map[string]struct{}{}, map[string]struct{}{}
	var result []CompiledRoute
	for _, definition := range config.Routes {
		service, err := toDomainService(definition)
		if err != nil {
			return nil, err
		}
		serviceBalancer, err := balancers.Build(*service, global.Params)
		if err != nil {
			return nil, fmt.Errorf("build balancer for service %q: %w", service.ID, err)
		}
		parsed, err := parseManifest(definition.ManifestPath)
		if err != nil {
			return nil, fmt.Errorf("service %q manifest: %w", service.ID, err)
		}
		pluginCache := make(map[string]echo.MiddlewareFunc)
		for _, route := range parsed.Routes {
			if _, exists := seenIDs[route.ID]; exists {
				return nil, fmt.Errorf("duplicate operationId %q", route.ID)
			}
			seenIDs[route.ID] = struct{}{}
			external := joinPrefix(definition.Endpoint, route.Path)
			echoPath := openAPIToEcho(external)
			key := route.Method + " " + echoPath
			if _, exists := seenRoutes[key]; exists {
				return nil, fmt.Errorf("duplicate route %s", key)
			}
			seenRoutes[key] = struct{}{}
			if err := validateUpstreamParameters(route.Path, route.Upstream); err != nil {
				return nil, fmt.Errorf("route %q: %w", route.ID, err)
			}
			middlewares, names, err := buildPlugins(route.Plugins, parsed.Plugins, pluginCache, plugins, serviceBalancer, global.Alias, config.Logging.RequestIDFieldName)
			if err != nil {
				return nil, fmt.Errorf("route %q: %w", route.ID, err)
			}
			result = append(result, CompiledRoute{
				Route:    domain.Route{ID: route.ID, Method: route.Method, ExternalPath: echoPath, UpstreamPattern: route.Upstream, Service: service, PluginNames: names},
				Balancer: serviceBalancer, Middlewares: middlewares,
			})
		}
	}
	return result, nil
}

func toDomainService(definition configs.Route) (*domain.Service, error) {
	service := &domain.Service{ID: definition.Name, Prefix: definition.Endpoint}
	for index, host := range definition.Backend.Hosts {
		parsed, err := url.Parse(host)
		if err != nil {
			return nil, fmt.Errorf("parse host %q: %w", host, err)
		}
		service.Instances = append(service.Instances, domain.Instance{ID: instanceID(definition.Endpoint, index), URL: parsed})
	}
	return service, nil
}

func instanceID(endpoint string, index int) string {
	name := strings.ReplaceAll(strings.Trim(endpoint, "/"), "/", "-")
	return fmt.Sprintf("%s-%d", name, index+1)
}

func buildPlugins(uses []pluginUse, definitions map[string]localPlugin, cache map[string]echo.MiddlewareFunc, registry *plugin.Registry, serviceBalancer balancer.Balancer, globalAlias, requestIDHeader string) ([]echo.MiddlewareFunc, []string, error) {
	balancerCount := 0
	var middlewares []echo.MiddlewareFunc
	var names []string
	for index, use := range uses {
		if use.Ref != "" {
			definition, exists := definitions[use.Ref]
			if !exists {
				return nil, nil, fmt.Errorf("unknown local plugin %q", use.Ref)
			}
			middleware := cache[use.Ref]
			if middleware == nil {
				var err error
				middleware, err = registry.Build(definition.Type, use.Ref, definition.Params)
				if err != nil {
					return nil, nil, err
				}
				cache[use.Ref] = middleware
			}
			middlewares = append(middlewares, middleware)
			names = append(names, use.Ref)
			continue
		}
		if use.Name != "balancer" {
			return nil, nil, fmt.Errorf("unsupported global plugin %q", use.Name)
		}
		balancerCount++
		if !use.InheritGlobal || use.InheritFromAlias != globalAlias {
			return nil, nil, errors.New("balancer must inherit the configured global alias")
		}
		if index != len(uses)-1 {
			return nil, nil, errors.New("balancer must be the last x-plugins element")
		}
		middlewares = append(middlewares, plugin.BalancerMiddleware(serviceBalancer, requestIDHeader))
		names = append(names, globalAlias)
	}
	if balancerCount != 1 {
		return nil, nil, errors.New("balancer must be connected exactly once")
	}
	return middlewares, names, nil
}

func parseManifest(path string) (manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := yaml.NewDecoder(file)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("multiple YAML documents are not allowed")
		}
		return manifest{}, err
	}
	root := document.Content
	if len(root) != 1 || root[0].Kind != yaml.MappingNode {
		return manifest{}, errors.New("manifest root must be a mapping")
	}
	rootMap := root[0]
	version := mappingValue(rootMap, "openapi")
	if version == nil || version.Kind != yaml.ScalarNode || !strings.HasPrefix(version.Value, "3.") {
		return manifest{}, errors.New("openapi 3.x is required")
	}
	result := manifest{Plugins: make(map[string]localPlugin)}
	if components := mappingValue(rootMap, "components"); components != nil {
		pluginNode := mappingValue(components, "x-plugins")
		if pluginNode != nil {
			if pluginNode.Kind != yaml.MappingNode {
				return manifest{}, errors.New("components.x-plugins must be a mapping")
			}
			for index := 0; index < len(pluginNode.Content); index += 2 {
				name, node := pluginNode.Content[index].Value, pluginNode.Content[index+1]
				if node.Kind != yaml.MappingNode {
					return manifest{}, fmt.Errorf("plugin %q must be a mapping", name)
				}
				if err := rejectUnknown(node, "type", "params"); err != nil {
					return manifest{}, fmt.Errorf("plugin %q: %w", name, err)
				}
				typeNode, params := mappingValue(node, "type"), mappingValue(node, "params")
				if typeNode == nil || params == nil {
					return manifest{}, fmt.Errorf("plugin %q requires type and params", name)
				}
				result.Plugins[name] = localPlugin{Type: typeNode.Value, Params: *params}
			}
		}
	}
	paths := mappingValue(rootMap, "paths")
	if paths == nil || paths.Kind != yaml.MappingNode {
		return manifest{}, errors.New("paths mapping is required")
	}
	for index := 0; index < len(paths.Content); index += 2 {
		path, item := paths.Content[index].Value, paths.Content[index+1]
		if !strings.HasPrefix(path, "/") || item.Kind != yaml.MappingNode {
			return manifest{}, fmt.Errorf("invalid OpenAPI path %q", path)
		}
		for operationIndex := 0; operationIndex < len(item.Content); operationIndex += 2 {
			method := strings.ToUpper(item.Content[operationIndex].Value)
			if !isMethod(method) {
				continue
			}
			operation := item.Content[operationIndex+1]
			if operation.Kind != yaml.MappingNode {
				return manifest{}, fmt.Errorf("%s %s operation must be a mapping", method, path)
			}
			for child := 0; child < len(operation.Content); child += 2 {
				key := operation.Content[child].Value
				if strings.HasPrefix(key, "x-") && key != "x-backend" && key != "x-plugins" {
					return manifest{}, fmt.Errorf("unknown gateway extension %q", key)
				}
			}
			idNode := mappingValue(operation, "operationId")
			if idNode == nil || strings.TrimSpace(idNode.Value) == "" {
				return manifest{}, fmt.Errorf("%s %s requires operationId", method, path)
			}
			backendNode := mappingValue(operation, "x-backend")
			upstream, err := parseBackend(backendNode)
			if err != nil {
				return manifest{}, fmt.Errorf("operation %q: %w", idNode.Value, err)
			}
			pluginNode := mappingValue(operation, "x-plugins")
			uses, err := parsePluginUses(pluginNode)
			if err != nil {
				return manifest{}, fmt.Errorf("operation %q: %w", idNode.Value, err)
			}
			result.Routes = append(result.Routes, manifestRoute{ID: idNode.Value, Method: method, Path: path, Upstream: upstream, Plugins: uses})
		}
	}
	if len(result.Routes) == 0 {
		return manifest{}, errors.New("manifest has no HTTP operations")
	}
	return result, nil
}

func parseBackend(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) != 1 {
		return "", errors.New("x-backend must contain exactly one element")
	}
	backend := node.Content[0]
	if backend.Kind != yaml.MappingNode {
		return "", errors.New("x-backend element must be a mapping")
	}
	if err := rejectUnknown(backend, "urlPattern"); err != nil {
		return "", err
	}
	pattern := mappingValue(backend, "urlPattern")
	if pattern == nil || !strings.HasPrefix(pattern.Value, "/") {
		return "", errors.New("x-backend.urlPattern must start with /")
	}
	return pattern.Value, nil
}

func parsePluginUses(node *yaml.Node) ([]pluginUse, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, errors.New("x-plugins sequence is required")
	}
	uses := make([]pluginUse, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			return nil, errors.New("x-plugins element must be a mapping")
		}
		if ref := mappingValue(item, "$ref"); ref != nil {
			if err := rejectUnknown(item, "$ref"); err != nil {
				return nil, err
			}
			const prefix = "#/components/x-plugins/"
			if !strings.HasPrefix(ref.Value, prefix) {
				return nil, fmt.Errorf("unsupported plugin reference %q", ref.Value)
			}
			uses = append(uses, pluginUse{Ref: strings.TrimPrefix(ref.Value, prefix)})
			continue
		}
		if err := rejectUnknown(item, "name", "inheritGlobal", "inheritFromAlias"); err != nil {
			return nil, err
		}
		var value struct {
			Name             string `yaml:"name"`
			InheritGlobal    bool   `yaml:"inheritGlobal"`
			InheritFromAlias string `yaml:"inheritFromAlias"`
		}
		if err := item.Decode(&value); err != nil {
			return nil, err
		}
		uses = append(uses, pluginUse{Name: value.Name, InheritGlobal: value.InheritGlobal, InheritFromAlias: value.InheritFromAlias})
	}
	return uses, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func rejectUnknown(node *yaml.Node, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for index := 0; index < len(node.Content); index += 2 {
		if _, ok := set[node.Content[index].Value]; !ok {
			return fmt.Errorf("unknown field %q", node.Content[index].Value)
		}
	}
	return nil
}

func isMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	}
	return false
}
func joinPrefix(prefix, path string) string {
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
}
func openAPIToEcho(path string) string { return pathParameterPattern.ReplaceAllString(path, `:$1`) }

func validateUpstreamParameters(external, upstream string) error {
	available := map[string]struct{}{}
	for _, match := range pathParameterPattern.FindAllStringSubmatch(external, -1) {
		available[match[1]] = struct{}{}
	}
	for _, match := range pathParameterPattern.FindAllStringSubmatch(upstream, -1) {
		if _, exists := available[match[1]]; !exists {
			return fmt.Errorf("upstream parameter %q is absent from external path", match[1])
		}
	}
	return nil
}
