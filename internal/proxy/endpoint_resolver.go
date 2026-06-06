package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// EndpointResolver resolves the endpoint a client wants to target out of an
// HTTP request, in priority order: HTTP header → special model-name prefix →
// query parameter.
type EndpointResolver struct {
	getEndpointsFunc func() []config.Endpoint
}

// NewEndpointResolver creates a resolver backed by a fixed endpoint slice.
func NewEndpointResolver(endpoints []config.Endpoint) *EndpointResolver {
	eps := endpoints
	return &EndpointResolver{
		getEndpointsFunc: func() []config.Endpoint {
			return eps
		},
	}
}

// NewEndpointResolverWithFunc creates a resolver that fetches the endpoint
// list dynamically through the provided callback (so config edits are picked
// up immediately).
func NewEndpointResolverWithFunc(getEndpointsFunc func() []config.Endpoint) *EndpointResolver {
	return &EndpointResolver{
		getEndpointsFunc: getEndpointsFunc,
	}
}

// ResolveEndpoint parses an incoming request and returns the targeted
// endpoint (or nil if none was specified), an optional model override, or an
// error if the client targeted a missing/disabled endpoint.
func (r *EndpointResolver) ResolveEndpoint(req *http.Request, bodyBytes []byte) (*config.Endpoint, string, error) {
	endpoints := r.getEndpointsFunc()

	// Priority 1: HTTP header
	if endpointName := r.parseEndpointFromHeader(req); endpointName != "" {
		endpoint := r.findEndpointByName(endpointName, endpoints)
		if endpoint == nil {
			return nil, "", fmt.Errorf("specified endpoint %q does not exist or is disabled", endpointName)
		}
		logger.Debug("[Resolver] endpoint specified via HTTP header: %s", endpointName)
		return endpoint, "", nil
	}

	// Priority 2: special model-name prefix
	var streamReq struct {
		Model string `json:"model"`
	}
	if len(bodyBytes) > 0 {
		json.Unmarshal(bodyBytes, &streamReq)
	}
	modelName := strings.TrimSpace(streamReq.Model)

	if modelName != "" && strings.HasPrefix(modelName, "@") {
		endpointName, modelOverride := r.parseEndpointFromModel(modelName)
		endpoint := r.findEndpointByName(endpointName, endpoints)
		if endpoint == nil {
			return nil, "", fmt.Errorf("specified endpoint %q does not exist or is disabled", endpointName)
		}
		logger.Debug("[Resolver] endpoint specified via model prefix: %s, model: %s", endpointName, modelOverride)
		return endpoint, modelOverride, nil
	}

	// Priority 3: query parameter
	if endpointName := r.parseEndpointFromQuery(req); endpointName != "" {
		endpoint := r.findEndpointByName(endpointName, endpoints)
		if endpoint == nil {
			return nil, "", fmt.Errorf("specified endpoint %q does not exist or is disabled", endpointName)
		}
		logger.Debug("[Resolver] endpoint specified via query parameter: %s", endpointName)
		return endpoint, "", nil
	}

	// Nothing specified — fall back to the default round-robin rotation.
	return nil, "", nil
}

// parseEndpointFromHeader extracts an endpoint name from supported headers:
// X-CCN-Endpoint, X-Endpoint-Name.
func (r *EndpointResolver) parseEndpointFromHeader(req *http.Request) string {
	if name := strings.TrimSpace(req.Header.Get("X-CCN-Endpoint")); name != "" {
		return name
	}
	if name := strings.TrimSpace(req.Header.Get("X-Endpoint-Name")); name != "" {
		return name
	}
	return ""
}

// parseEndpointFromModel parses the @endpoint[/model] prefix:
//
//	@endpoint-name/model-name → ("endpoint-name", "model-name")
//	@endpoint-name            → ("endpoint-name", "")
func (r *EndpointResolver) parseEndpointFromModel(model string) (string, string) {
	model = strings.TrimSpace(model)
	if !strings.HasPrefix(model, "@") {
		return "", ""
	}

	model = model[1:]

	slashIndex := strings.Index(model, "/")
	if slashIndex == -1 {
		// Form: @endpoint-name
		endpointName := strings.TrimSpace(model)
		return endpointName, ""
	}

	// Form: @endpoint-name/model-name
	endpointName := strings.TrimSpace(model[:slashIndex])
	modelName := strings.TrimSpace(model[slashIndex+1:])
	return endpointName, modelName
}

// parseEndpointFromQuery extracts an endpoint name from supported query
// parameters: `endpoint`, `ep`.
func (r *EndpointResolver) parseEndpointFromQuery(req *http.Request) string {
	if name := strings.TrimSpace(req.URL.Query().Get("endpoint")); name != "" {
		return name
	}
	if name := strings.TrimSpace(req.URL.Query().Get("ep")); name != "" {
		return name
	}
	return ""
}

// findEndpointByName looks up an enabled endpoint by name (case-insensitive).
func (r *EndpointResolver) findEndpointByName(name string, endpoints []config.Endpoint) *config.Endpoint {
	targetName := strings.ToLower(strings.TrimSpace(name))

	for i := range endpoints {
		endpoint := &endpoints[i]
		if !endpoint.Enabled {
			continue
		}
		if strings.ToLower(strings.TrimSpace(endpoint.Name)) == targetName {
			return endpoint
		}
	}
	return nil
}
