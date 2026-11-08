package domain

import (
	"net/http"
	"net/url"
	"time"
)

const (
	ContextRouteID    = "gateway.route_id"
	ContextServiceID  = "gateway.service_id"
	ContextInstanceID = "gateway.instance_id"
	ContextPlugins    = "gateway.plugins"
	ContextUpstream   = "gateway.upstream"
)

type Instance struct {
	ID  string
	URL *url.URL
}

type Service struct {
	ID        string
	Prefix    string
	Instances []Instance
}

type RequestMeta struct {
	RequestID string
	RemoteIP  string
	Headers   http.Header
}

type Route struct {
	ID              string
	Method          string
	ExternalPath    string
	UpstreamPattern string
	Service         *Service
	PluginNames     []string
}

type UpstreamResult struct {
	Duration   time.Duration
	StatusCode int
	Err        error
}
