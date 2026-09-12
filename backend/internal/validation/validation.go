// Package validation centralizes request-body validation and error responses.
// It configures the Gin validator to report JSON field names and renders a
// consistent ErrorResponse envelope for binding and business-rule failures.
package validation

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// init registers the custom validators/tag-name function for every binary and
// test that imports this package, independent of whether Init has been called
// yet. Init remains the documented startup hook and is idempotent.
func init() { Init() }

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
		// maxbytes measures raw UTF-8 bytes (unlike the built-in `max`, which
		// counts runes). bcrypt only consumes the first 72 bytes, so this caps
		// password length accurately for multi-byte passwords.
		_ = v.RegisterValidation("maxbytes", maxBytes)
	}
}

// maxBytes reports whether the field's byte length is within the configured
// limit (default 72). It backs the `maxbytes` validation tag.
func maxBytes(fl validator.FieldLevel) bool {
	limit := 72
	if raw := fl.Param(); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	return len(fl.Field().String()) <= limit
}
