package main

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// openAPISpec is the embedded OpenAPI document served at GET /api/v1/openapi.yaml.
// It is generated/maintained by hand alongside the handlers; openapi_test.go
// fails the build when a registered route is missing from the document, so the
// contract cannot silently drift from the router.
//
//go:embed openapi.yaml
var openAPISpec []byte

// serveOpenAPISpec writes the embedded OpenAPI document. It is a public route
// so clients and tooling (codegen, docs, tests) can fetch the contract without
// authenticating.
func serveOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", openAPISpec)
}
