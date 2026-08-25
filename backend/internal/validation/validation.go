// Package validation centralizes request-body validation and error responses.
// It configures the Gin validator to report JSON field names and renders a
// consistent ErrorResponse envelope for binding and business-rule failures.
package validation

import (
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Init should be called once at application startup (e.g. from main()).
func Init() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name, _, _ := strings.Cut(fld.Tag.Get("json"), ",")
			if name == "-" {
				return ""
			}
			return name
		})
	}
}
