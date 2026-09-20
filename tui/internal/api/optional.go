package api

import (
	"encoding/json"
)

// The API's partial-update bodies (PATCH /transactions/:id, PUT /accounts/:id,
// PUT /recurring/:id, PUT /paperless/settings) distinguish three states for a
// nullable field: the key is absent (leave the stored value alone), the key
// carries a value (set it), or the key is an explicit null (clear the column).
// OptionalUUID and OptionalInt mirror backend/models.OptionalUUID and
// OptionalInt so the TUI can express all three. The zero value means "absent",
// and every request field carrying one is tagged `omitzero`, not `omitempty`:
// encoding/json cannot omit a struct, so `omitempty` would serialize the zero
// value as an explicit null and clear the column on an unrelated update.
// `omitzero` consults these types' IsZero method, which is why it exists here.

// OptionalUUID is an absent / set / explicitly-null string identifier.
type OptionalUUID struct {
	value *string
	set   bool
}

// UUID returns an OptionalUUID carrying a value.
func UUID(value string) OptionalUUID { return OptionalUUID{value: &value, set: true} }

// UUIDNull returns an OptionalUUID that marshals as an explicit null.
func UUIDNull() OptionalUUID { return OptionalUUID{set: true} }

// IsZero reports whether the key should be omitted entirely.
func (o OptionalUUID) IsZero() bool { return !o.set }

// Set reports whether the key is present in the outgoing body.
func (o OptionalUUID) Set() bool { return o.set }

// Value returns the identifier, or nil for an explicit null / absent key.
func (o OptionalUUID) Value() *string { return o.value }

// MarshalJSON emits the value, or null when it is explicitly cleared.
func (o OptionalUUID) MarshalJSON() ([]byte, error) {
	if o.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.value)
}

// OptionalInt is an absent / set / explicitly-null integer.
type OptionalInt struct {
	value *int
	set   bool
}

// Int returns an OptionalInt carrying a value.
func Int(value int) OptionalInt { return OptionalInt{value: &value, set: true} }

// IntNull returns an OptionalInt that marshals as an explicit null.
func IntNull() OptionalInt { return OptionalInt{set: true} }

// IsZero reports whether the key should be omitted entirely.
func (o OptionalInt) IsZero() bool { return !o.set }

// Set reports whether the key is present in the outgoing body.
func (o OptionalInt) Set() bool { return o.set }

// Value returns the number, or nil for an explicit null / absent key.
func (o OptionalInt) Value() *int { return o.value }

// MarshalJSON emits the value, or null when it is explicitly cleared.
func (o OptionalInt) MarshalJSON() ([]byte, error) {
	if o.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.value)
}

// derefOr reads an optional value for display, using fallback when unset.
func derefOr[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}
