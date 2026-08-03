package models

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestOptionalUUID(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	var withValue, withNull, absent UpdateTransactionRequest
	assert.NoError(t, json.Unmarshal([]byte(`{"categoryId":"`+id.String()+`"}`), &withValue))
	assert.NoError(t, json.Unmarshal([]byte(`{"categoryId":null}`), &withNull))
	assert.NoError(t, json.Unmarshal([]byte(`{}`), &absent))

	assert.True(t, withValue.CategoryID.Set())
	assert.Equal(t, id, *withValue.CategoryID.Value())

	assert.True(t, withNull.CategoryID.Set())
	assert.Nil(t, withNull.CategoryID.Value())

	assert.False(t, absent.CategoryID.Set())
	assert.Nil(t, absent.CategoryID.Value())
}
