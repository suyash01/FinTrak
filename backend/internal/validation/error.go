package validation

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// RespondBindError writes a consistent error envelope for any ShouldBindJSON error.
func RespondBindError(c *gin.Context, err error) {
	if fieldErrs := FormatValidationErrors(err); fieldErrs != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Errors: fieldErrs})
		return
	}
	c.JSON(http.StatusBadRequest, models.ErrorResponse{
		Errors: []models.FieldError{{Message: "invalid request body"}},
	})
}

func RespondError(c *gin.Context, message string, status int) {
	c.JSON(status, models.ErrorResponse{
		Errors: []models.FieldError{{Message: message}},
	})
}

func RespondAuthError(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
		Errors: []models.FieldError{{Message: message}},
	})
}

// FormatValidationErrors converts validator errors into a client-friendly slice.
func FormatValidationErrors(err error) []models.FieldError {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return nil
	}

	out := make([]models.FieldError, 0, len(ve))
	for _, fe := range ve {
		out = append(out, models.FieldError{
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
