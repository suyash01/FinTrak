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

func TestOptionalInt(t *testing.T) {
	var withValue, withNull, absent UpdateUserSettingsRequest
	assert.NoError(t, json.Unmarshal([]byte(`{"pageSize":25}`), &withValue))
	assert.NoError(t, json.Unmarshal([]byte(`{"pageSize":null}`), &withNull))
	assert.NoError(t, json.Unmarshal([]byte(`{}`), &absent))

	assert.True(t, withValue.PageSize.Set())
	assert.Equal(t, 25, *withValue.PageSize.Value())

	assert.True(t, withNull.PageSize.Set())
	assert.Nil(t, withNull.PageSize.Value())

	assert.False(t, absent.PageSize.Set())
	assert.Nil(t, absent.PageSize.Value())
}

func TestOptionalUUIDNilReceiver(t *testing.T) {
	var o *OptionalUUID
	assert.False(t, o.Set())
	assert.Nil(t, o.Value())
}

func TestOptionalIntNilReceiver(t *testing.T) {
	var o *OptionalInt
	assert.False(t, o.Set())
	assert.Nil(t, o.Value())
}
