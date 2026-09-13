package validation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleRequest exercises every message branch FormatValidationErrors renders.
// Gin v1.12 configures the validator tag name as "binding", so that is the tag
// the request models (and these fixtures) must use.
type sampleRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,maxbytes=72"`
	Role     string `json:"role" binding:"oneof=admin user"`
	Nick     string `json:"nick" binding:"max=5"`
}

type urlRequest struct {
	URL string `json:"url" binding:"url"`
}

func validateStruct(t *testing.T, v any) error {
	t.Helper()
	val, ok := binding.Validator.Engine().(*validator.Validate)
	require.True(t, ok, "gin binding validator must be a *validator.Validate")
	return val.Struct(v)
}

func TestFormatValidationErrorsUsesJSONFieldNamesAndMessages(t *testing.T) {
	err := validateStruct(t, sampleRequest{})
	require.Error(t, err)

	got := FormatValidationErrors(err)
	require.Len(t, got, 3)

	assert.Equal(t, models.FieldError{Field: "email", Tag: "required", Message: "email is required"}, got[0])
	assert.Equal(t, models.FieldError{Field: "password", Tag: "required", Message: "password is required"}, got[1])
	assert.Equal(t, models.FieldError{Field: "role", Tag: "oneof", Message: "role must be one of [admin user]"}, got[2])
}

func TestFormatValidationErrorsRendersBoundsMessages(t *testing.T) {
	err := validateStruct(t, sampleRequest{
		Email:    "not-an-email",
		Password: "short",
		Role:     "admin",
		Nick:     "toolong",
	})
	require.Error(t, err)

	got := FormatValidationErrors(err)
	require.Len(t, got, 3)

	assert.Equal(t, "email must be a valid email", got[0].Message)
	assert.Equal(t, "password must be at least 8 characters", got[1].Message)
	assert.Equal(t, "nick must be at most 5 characters", got[2].Message)
}

func TestFormatValidationErrorsMaxBytes(t *testing.T) {
	// 73 ASCII bytes exceeds the 72-byte cap; min=8 still passes.
	err := validateStruct(t, sampleRequest{
		Email:    "a@b.com",
		Password: strings.Repeat("a", 73),
		Role:     "user",
	})
	require.Error(t, err)

	got := FormatValidationErrors(err)
	require.Len(t, got, 1)
	assert.Equal(t, "password", got[0].Field)
	assert.Equal(t, "maxbytes", got[0].Tag)
	assert.Equal(t, "password must be at most 72 bytes", got[0].Message)
}

func TestFormatValidationErrorsUnknownTagFallsBackToInvalid(t *testing.T) {
	err := validateStruct(t, urlRequest{URL: "not a url"})
	require.Error(t, err)

	got := FormatValidationErrors(err)
	require.Len(t, got, 1)
	assert.Equal(t, "url is invalid", got[0].Message)
}

func TestFormatValidationErrorsIgnoresNonValidationError(t *testing.T) {
	assert.Nil(t, FormatValidationErrors(errors.New("plain failure")))
}

func TestRespondBindErrorValidation(t *testing.T) {
	err := validateStruct(t, sampleRequest{Email: "", Password: "", Role: "admin"})
	require.Error(t, err)

	c, w := newTestContext()
	RespondBindError(c, err)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := decodeErrorResponse(t, w.Body.Bytes())
	require.Len(t, resp.Errors, 2)
	assert.Equal(t, "email", resp.Errors[0].Field)
	assert.Equal(t, "email is required", resp.Errors[0].Message)
}

func TestRespondBindErrorGeneric(t *testing.T) {
	c, w := newTestContext()
	RespondBindError(c, errors.New("unexpected EOF"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := decodeErrorResponse(t, w.Body.Bytes())
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "invalid request body", resp.Errors[0].Message)
}

func TestRespondError(t *testing.T) {
	c, w := newTestContext()
	RespondError(c, "conflict", http.StatusConflict)

	assert.Equal(t, http.StatusConflict, w.Code)
	resp := decodeErrorResponse(t, w.Body.Bytes())
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "conflict", resp.Errors[0].Message)
}

func TestRespondAuthErrorAborts(t *testing.T) {
	c, w := newTestContext()
	RespondAuthError(c, "missing token")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
	resp := decodeErrorResponse(t, w.Body.Bytes())
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "missing token", resp.Errors[0].Message)
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

func decodeErrorResponse(t *testing.T, body []byte) models.ErrorResponse {
	t.Helper()
	var resp models.ErrorResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp
}
