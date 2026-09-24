package main

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// openAPISpec is the embedded OpenAPI document served at GET /api/v1/openapi.yaml.
// It is maintained by hand alongside the handlers. openapi_test.go fails when
// a registered route is missing or the document advertises a route that no
// longer exists; schemas, defaults, status codes, and descriptions still need
// semantic review when the contract changes.
//
//go:embed openapi.yaml
var openAPISpec []byte

// serveOpenAPISpec writes the embedded OpenAPI document. It is a public route
// so clients and tooling (codegen, docs, tests) can fetch the contract without
// authenticating.
func serveOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", openAPISpec)
}
