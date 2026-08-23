package validation

import (
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
)

type FieldError struct {
	Field   string `json:"field"`
	Tag     string `json:"tag"`
	Message string `json:"message"`
}

// FormatValidationErrors converts validator errors into a client-friendly slice.
func FormatValidationErrors(err error) []FieldError {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return nil
	}

	out := make([]FieldError, 0, len(ve))
	for _, fe := range ve {
		out = append(out, FieldError{
			Field:   fe.Field(),
			Tag:     fe.Tag(),
			Message: message(fe),
		})
	}
	return out
}

func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case "email":
		return fmt.Sprintf("%s must be a valid email", fe.Field())
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", fe.Field(), fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", fe.Field(), fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of [%s]", fe.Field(), fe.Param())
	default:
		return fmt.Sprintf("%s is invalid", fe.Field())
	}
}
