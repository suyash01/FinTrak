package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

// openAPIDocument is the minimal shape of openapi.yaml needed to guard route
// coverage. Method blocks are decoded as raw maps so we only care about keys.
type openAPIDocument struct {
	OpenAPI string                            `yaml:"openapi"`
	Paths   map[string]map[string]interface{} `yaml:"paths"`
}

// ginParamRE converts Gin's ":id" path params to OpenAPI's "{id}".
var ginParamRE = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// openAPIPath maps a registered Gin route to its openapi.yaml path key (the
// spec paths are relative to the /api/v1 server base).
func openAPIPath(ginPath string) string {
	p := strings.TrimPrefix(ginPath, "/api/v1")
	return ginParamRE.ReplaceAllString(p, `{$1}`)
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "options": true, "head": true,
}

// TestOpenAPICoversRegisteredRoutes fails when a handler is added or renamed
// without updating openapi.yaml.
func TestOpenAPICoversRegisteredRoutes(t *testing.T) {
	var doc openAPIDocument
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))
	require.NotEmpty(t, doc.OpenAPI, "openapi.yaml is missing the openapi version")
	require.NotEmpty(t, doc.Paths, "openapi.yaml has no paths")

	var missing []string
	for _, route := range testRouter().Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		key := openAPIPath(route.Path)
		methods, ok := doc.Paths[key]
		if !ok {
			missing = append(missing, route.Method+" "+route.Path)
			continue
		}
		if _, ok = methods[strings.ToLower(route.Method)]; !ok {
			missing = append(missing, route.Method+" "+route.Path)
		}
	}
	assert.Emptyf(t, missing, "routes missing from openapi.yaml: %v", missing)
}

// TestOpenAPIDocumentsOnlyRegisteredRoutes fails when the spec advertises an
// endpoint that no longer exists.
func TestOpenAPIDocumentsOnlyRegisteredRoutes(t *testing.T) {
	var doc openAPIDocument
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))

	registered := make(map[string]map[string]bool)
	for _, route := range testRouter().Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		key := openAPIPath(route.Path)
		if registered[key] == nil {
			registered[key] = map[string]bool{}
		}
		registered[key][strings.ToLower(route.Method)] = true
	}

	for path, methods := range doc.Paths {
		for method := range methods {
			if !httpMethods[method] {
				continue
			}
			require.Truef(t, registered[path][method],
				"openapi.yaml documents %s %s but no matching route is registered",
				strings.ToUpper(method), path)
		}
	}
}

// TestServeOpenAPISpec checks the embedded document is actually served.
func TestServeOpenAPISpec(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	testRouter().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "yaml")
	assert.Contains(t, w.Body.String(), "openapi:")
}
