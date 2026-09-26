package query

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
)

// fieldDef is the single definition of one field. The parser and the compiler
// both read this table, and the frontend's autocomplete mirrors it, so a field
// cannot be suggested that the parser will reject.
type fieldDef struct {
	// userTyped is false for FieldPlain, which the parser synthesises. A user
	// typing `plain:coffee` gets unknown_field.
	userTyped bool
	// ops is the legal operator set.
	ops []Op
	// enum is the fixed value domain, if the field has one.
	enum []string
	// kind selects the value validator.
	kind valueKind
	// sentinels are the words that stand for an absent value. Only a field whose
	// column is actually nullable may use them, because the compiler turns one
	// into `IS NULL`; on a non-nullable column it would bind the literal string
	// against the column and PostgreSQL would answer with a type error.
	sentinels []string
}

type valueKind int

const (
	kindText valueKind = iota
	kindUUID
	kindEnum
	kindAmount
	kindDate
	kindTagList
)

var (
	opsEq    = []Op{OpEq, OpNe}
	opsOrder = []Op{OpEq, OpNe, OpGt, OpGe, OpLt, OpLe}
	opsText  = []Op{OpEq, OpNe, OpLike}

	// nullSentinels are the two words the existing filters already use for an
	// absent category and an absent payee. Only cat and payee may take them.
	nullSentinels = []string{"none", "uncategorized"}
)

var fieldTable = map[string]fieldDef{
	FieldPlain:  {kind: kindText, ops: opsText},
	"desc":      {userTyped: true, kind: kindText, ops: opsText},
	"note":      {userTyped: true, kind: kindText, ops: opsText},
	"cat":       {userTyped: true, kind: kindUUID, ops: opsEq, sentinels: nullSentinels},
	"group":     {userTyped: true, kind: kindUUID, ops: opsEq},
	"acct":      {userTyped: true, kind: kindUUID, ops: opsEq},
	"payee":     {userTyped: true, kind: kindUUID, ops: opsEq, sentinels: nullSentinels},
	"tag":       {userTyped: true, kind: kindTagList, ops: opsEq},
	"type":      {userTyped: true, kind: kindEnum, enum: []string{"debit", "credit"}, ops: opsEq},
	"linked":    {userTyped: true, kind: kindEnum, enum: []string{"true", "false"}, ops: opsEq},
	"recurring": {userTyped: true, kind: kindEnum, enum: []string{"linked", "unlinked"}, ops: opsEq},
	"amt":       {userTyped: true, kind: kindAmount, ops: opsOrder},
	"date":      {userTyped: true, kind: kindDate, ops: opsOrder},
}

// userField looks up a field the user may type. The parser never accepts
// FieldPlain as input.
func userField(name string) (fieldDef, bool) {
	def, ok := fieldTable[name]
	if !ok || !def.userTyped {
		return fieldDef{}, false
	}
	return def, true
}

func (d fieldDef) allowsOp(op Op) bool {
	for _, allowed := range d.ops {
		if allowed == op {
			return true
		}
	}
	return false
}

// fieldError is a validation failure carrying the diagnostic code it surfaces
// as, so Parse never has to classify errors itself.
type fieldError struct {
	code    string
	message string
}

func (e *fieldError) toDiagnostic(term string, position int) Diagnostic {
	return Diagnostic{Term: term, Code: e.code, Message: e.message, Position: position}
}

// Error makes fieldError a plain error, so validate can return the `error`
// interface while Parse recovers the code through a type assertion.
func (e *fieldError) Error() string { return e.message }

// toDiagnostic recovers the diagnostic from a validate error. A non-fieldError
// is reported as an unresolvable value rather than dropped silently, so an
// error can never escape without a code.
func toDiagnostic(err error, term string, position int) Diagnostic {
	var fe *fieldError
	if errors.As(err, &fe) {
		return fe.toDiagnostic(term, position)
	}
	return Diagnostic{Term: term, Code: CodeUnresolved, Message: err.Error(), Position: position}
}

func errf(code, format string, args ...any) *fieldError {
	return &fieldError{code: code, message: fmt.Sprintf(format, args...)}
}

// validate checks one term's values against the field's kind, returning a
// *fieldError or nil. A multi-value term is an OR, so every value must be
// individually well-formed.
func (d fieldDef) validate(t Term) error {
	if len(t.Values) == 0 {
		return errf(CodeMissingValue, "%s: needs a value", t.Field)
	}
	for _, v := range t.Values {
		switch d.kind {
		case kindText:
			// Any text is acceptable; `~` is a substring match.
		case kindUUID:
			if contains(d.sentinels, v) {
				continue
			}
			if isUUID(v) {
				continue
			}
			// Say what this field actually accepts. Telling someone to try
			// "none" on a field with no nullable column would send them round in
			// circles, and would have 500ed if the validator had allowed it.
			if len(d.sentinels) == 0 {
				return errf(CodeUnresolved, "%s takes an id (a uuid); %q is not one", t.Field, v)
			}
			return errf(CodeUnresolved, "%s: %q is not an id this server can resolve; ids are a uuid, %q or %q", t.Field, v, d.sentinels[0], d.sentinels[1])
		case kindEnum:
			if !contains(d.enum, v) {
				return errf(CodeUnresolved, "%s: %q is not one of %v", t.Field, v, d.enum)
			}
		case kindAmount:
			if _, err := money.Parse(v); err != nil {
				return errf(CodeMalformedAmt, "%s: %q is not an amount (e.g. 50 or 50.75)", t.Field, v)
			}
		case kindDate:
			// The same window every write edge applies, so a typo'd year is
			// reported instead of quietly matching nothing.
			if _, msg := validation.CheckTransactionDate(v, time.Now()); msg != "" {
				return errf(CodeMalformedDate, "%s: %q is not a usable date (%s)", t.Field, v, msg)
			}
		case kindTagList:
			// Tags are free text; there is no tag table to resolve against.
			if strings.Contains(v, "'") {
				return errf(CodeUnresolved, "%s: a tag name cannot contain a quote", t.Field)
			}
		}
	}
	return nil
}

func isUUID(v string) bool {
	_, err := uuid.Parse(v)
	return err == nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
