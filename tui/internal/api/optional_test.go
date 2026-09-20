package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOptionalFieldsDistinguishAbsentFromNull pins the three-state contract the
// API's partial updates rely on: an absent key leaves the stored value alone, a
// value sets it, and an explicit null clears the column. Go's `omitempty` does
// NOT omit a struct, so the types must be tagged `omitzero`, which honors their
// IsZero method — without it every unset field would silently serialize as an
// explicit null and clear the column on every unrelated update.
func TestOptionalFieldsDistinguishAbsentFromNull(t *testing.T) {
	type payload struct {
		CategoryID OptionalUUID `json:"categoryId,omitzero"`
		PayeeID    OptionalUUID `json:"payeeId,omitzero"`
		BillingDay OptionalInt  `json:"billingDay,omitzero"`
		PageSize   OptionalInt  `json:"pageSize,omitzero"`
	}

	t.Run("unset fields are absent from the body", func(t *testing.T) {
		data, err := json.Marshal(payload{})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != `{}` {
			t.Errorf("marshal = %s, want {} — an unset field must not be sent at all", data)
		}
	})

	t.Run("set fields carry their value", func(t *testing.T) {
		data, err := json.Marshal(payload{CategoryID: UUID("cat-1"), PageSize: Int(50)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(data), `"categoryId":"cat-1"`) {
			t.Errorf("marshal = %s, want the category id", data)
		}
		if !strings.Contains(string(data), `"pageSize":50`) {
			t.Errorf("marshal = %s, want the page size", data)
		}
		if strings.Contains(string(data), "payeeId") || strings.Contains(string(data), "billingDay") {
			t.Errorf("marshal = %s, want untouched fields omitted", data)
		}
	})

	t.Run("explicit nulls clear the column", func(t *testing.T) {
		data, err := json.Marshal(payload{CategoryID: UUIDNull(), BillingDay: IntNull()})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(data), `"categoryId":null`) {
			t.Errorf("marshal = %s, want an explicit null to clear the category", data)
		}
		if !strings.Contains(string(data), `"billingDay":null`) {
			t.Errorf("marshal = %s, want an explicit null to clear the billing day", data)
		}
		if strings.Contains(string(data), "payeeId") || strings.Contains(string(data), "pageSize") {
			t.Errorf("marshal = %s, want untouched fields omitted", data)
		}
	})

	t.Run("IsZero agrees with what is sent", func(t *testing.T) {
		if !(OptionalUUID{}).IsZero() || !(OptionalInt{}).IsZero() {
			t.Error("the zero values must report themselves as zero so omitzero drops them")
		}
		if UUID("x").IsZero() || UUIDNull().IsZero() || Int(1).IsZero() || IntNull().IsZero() {
			t.Error("a set or explicitly-cleared field must not report itself as zero")
		}
	})
}

// TestUpdateRequestsOmitUntouchedFields checks the real request bodies, since a
// stale tag there is what would clear a user's data.
func TestUpdateRequestsOmitUntouchedFields(t *testing.T) {
	data, err := json.Marshal(UpdateTransactionRequest{Description: new("coffee")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"categoryId", "payeeId", "billingCycleId", "tags", "date", "amount"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("PATCH body %s carries %q, which would overwrite it", data, key)
		}
	}
	if !strings.Contains(string(data), `"description":"coffee"`) {
		t.Errorf("PATCH body %s lost the field the user actually changed", data)
	}

	data, err = json.Marshal(UpdateAccountRequest{BillingDay: IntNull()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"billingDay":null`) {
		t.Errorf("clearing the billing day produced %s", data)
	}

	data, err = json.Marshal(UpdateUserSettingsRequest{PaperlessTag: new("bills")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "pageSize") {
		t.Errorf("settings body %s would clear the stored page size", data)
	}
}
