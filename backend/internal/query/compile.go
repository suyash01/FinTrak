package query

import (
	"fmt"
	"strings"

	"github.com/fintrak/backend/internal/money"
)

// Sink is what the compiler appends predicates to. *handlers.txnFilter
// satisfies it through a thin adapter (handlers/query_sink.go) so this package
// never has to import handlers, and so the heavily-tested filter builder keeps
// its existing unexported method names.
//
// The five methods mirror txnFilter's param/clause/anyOf/params/raw exactly,
// including the placeholder-numbering contract: the first appended value is
// $2, because args[0] is always the user id and where() renders
// " WHERE t.user_id = $1".
type Sink interface {
	Param(clause string, value any)
	Clause(format string, value any) string
	AnyOf(clauses []string)
	Params(clause string, values ...any)
	Raw(clause string)
}

// Compile appends every term's predicate to sink, AND-ed with whatever the
// caller already appended. Every fragment references only `transactions t` —
// using correlated EXISTS instead of a join wherever a related table is
// needed — because the list query and its COUNT(*) share this predicate, and a
// fragment naming a joined table would make the count fail while the page
// rendered fine.
//
// A term that cannot be emitted is dropped and returned. Compile never panics
// and never fails the request.
func Compile(e Expr, sink Sink) []Diagnostic {
	var diags []Diagnostic
	for _, t := range e.Terms {
		if d := emit(t, sink); d != nil {
			diags = append(diags, *d)
		}
	}
	return diags
}

// emit renders one term. A *Diagnostic return means "dropped, and report this".
func emit(t Term, sink Sink) *Diagnostic {
	// `not x` and `x!=v` are the same request, so both spellings negate. This is
	// why no emitter below has to consider Op for the ne case.
	negate := t.Negated || t.Op == OpNe

	switch t.Field {
	case FieldPlain:
		// The existing free-text search, one AND-ed four-way group per word, so
		// `coffee shop` requires both words. Each group binds the same pattern
		// four times, exactly as txnQueryFilter's search does today.
		for _, w := range t.Values {
			pattern := "%" + EscapeLikePattern(w) + "%"
			sink.Params(`(LOWER(t.description) LIKE LOWER($%d)
				  OR LOWER(COALESCE(t.notes, '')) LIKE LOWER($%d)
				  OR EXISTS (SELECT 1 FROM payees sp WHERE sp.id = t.payee_id AND LOWER(sp.name) LIKE LOWER($%d))
				  OR EXISTS (SELECT 1 FROM unnest(t.tags) AS tag WHERE LOWER(tag) LIKE LOWER($%d)))`,
				pattern, pattern, pattern, pattern)
		}
		return nil

	case "desc":
		return emitContains(t, sink, "LOWER(t.description) LIKE LOWER($%d)", negate)
	case "note":
		return emitContains(t, sink, "LOWER(COALESCE(t.notes, '')) LIKE LOWER($%d)", negate)

	case "cat":
		return emitColumn(t, sink, "t.category_id = $%d", "t.category_id IS NULL", negate)
	case "acct":
		return emitColumn(t, sink, "t.account_id = $%d", "", negate)
	case "payee":
		return emitColumn(t, sink, "t.payee_id = $%d", "t.payee_id IS NULL", negate)
	case "type":
		return emitColumn(t, sink, "t.type = $%d", "", negate)
	case "group":
		return emitGroup(t, sink, negate)
	case "tag":
		return emitTags(t, sink, negate)
	case "linked":
		return emitFlag(t, sink, negate,
			"EXISTS (SELECT 1 FROM links l WHERE l.from_txn_id = t.id OR l.to_txn_id = t.id)",
			"NOT EXISTS (SELECT 1 FROM links l WHERE l.from_txn_id = t.id OR l.to_txn_id = t.id)")
	case "recurring":
		return emitFlag(t, sink, negate,
			"EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id)",
			"NOT EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id)")
	case "amt":
		return emitOrdered(t, sink, "t.amount", negate, CodeMalformedAmt, convertAmount)
	case "date":
		return emitOrdered(t, sink, "t.date", negate, CodeMalformedDate, convertDate)
	}
	return &Diagnostic{Term: t.Raw, Code: CodeUnknownField, Message: "unknown field " + t.Field, Position: t.Position}
}

// emitContains handles desc and note. Every operator is a contains: a user
// typing `desc:rent` means "containing rent", and `~` is just the explicit
// spelling of the same thing.
func emitContains(t Term, sink Sink, format string, negate bool) *Diagnostic {
	for _, v := range t.Values {
		if negate {
			sink.Params("NOT ("+format+")", "%"+EscapeLikePattern(v)+"%")
			continue
		}
		sink.Params(format, "%"+EscapeLikePattern(v)+"%")
	}
	return nil
}

// emitColumn emits one predicate per value, OR-ed, for a plain column. isNull
// is the sentinel fragment for "none"/"uncategorized", and empty for columns
// that have no such sentinel.
func emitColumn(t Term, sink Sink, format, isNull string, negate bool) *Diagnostic {
	clauses := make([]string, 0, len(t.Values))
	for _, v := range t.Values {
		if isNull != "" && (v == "none" || v == "uncategorized") {
			clauses = append(clauses, isNull)
			continue
		}
		clauses = append(clauses, sink.Clause(format, v))
	}
	wrapGroup(sink, clauses, negate)
	return nil
}

// emitGroup matches every category in a group through a correlated EXISTS, so
// the fragment still names only `transactions t`.
func emitGroup(t Term, sink Sink, negate bool) *Diagnostic {
	clauses := make([]string, 0, len(t.Values))
	for _, v := range t.Values {
		clauses = append(clauses, sink.Clause("EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $%d)", v))
	}
	wrapGroup(sink, clauses, negate)
	return nil
}

// emitTags binds the tag names as a one-element text[] literal and uses array
// overlap, so any of the tags matches. Tags are names, not ids: there is no tag
// table to resolve against, which is why the frontend passes tag names straight
// through.
//
// This is the one place the compiler puts user text into the statement rather
// than binding it, because the existing `tags` parameter binds a []string and a
// []string driver value makes the emitted SQL unreadable in a log. The parser
// therefore refuses a tag containing a single quote (fields.go, kindTagList),
// which is the only way out of a literal here.
func emitTags(t Term, sink Sink, negate bool) *Diagnostic {
	if len(t.Values) == 0 {
		return &Diagnostic{Term: t.Raw, Code: CodeMissingValue, Message: "tag: needs a value", Position: t.Position}
	}
	literal := "ARRAY[" + quoteAll(t.Values) + "]::text[]"
	if negate {
		sink.Raw("NOT (t.tags && " + literal + ")")
		return nil
	}
	sink.Raw("t.tags && " + literal)
	return nil
}

func quoteAll(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, ", ")
}

// emitFlag is the boolean field: linked / recurring choose between two fixed
// EXISTS fragments and bind nothing, so they use Raw.
func emitFlag(t Term, sink Sink, negate bool, whenTrue, whenFalse string) *Diagnostic {
	for _, v := range t.Values {
		fragment := whenTrue
		if v == "false" || v == "unlinked" {
			fragment = whenFalse
		}
		if negate {
			// Negating flips the sense, so `not linked:true` is the false
			// branch rather than a NOT wrapped around it.
			fragment = whenFalse
			if v == "false" || v == "unlinked" {
				fragment = whenTrue
			}
		}
		sink.Raw(fragment)
	}
	return nil
}

// convertAmount turns decimal major units into integer minor units with
// money.Parse — never a float — so `amt>50` binds 5000 for the BIGINT cents
// column. A nil return means the value is unusable.
func convertAmount(v string) (any, bool) {
	amount, err := money.Parse(v)
	if err != nil {
		return nil, false
	}
	return amount.Cents(), true
}

// convertDate passes a date through: the parser already validated its shape and
// its position in the transaction date window, so anything reaching the compiler
// is well-formed.
func convertDate(v string) (any, bool) { return v, true }

// emitOrdered is the shared body of the two comparable fields, amt and date. They
// differ only in the conversion, and having one copy is what stops the money
// conversion from drifting between them.
func emitOrdered(t Term, sink Sink, column string, negate bool, badValueCode string, convert func(string) (any, bool)) *Diagnostic {
	op, ok := cmpFormat(t.Op)
	if !ok {
		return &Diagnostic{Term: t.Raw, Code: CodeBadOperator, Message: t.Field + " does not support " + string(t.Op), Position: t.Position}
	}
	clauses := make([]string, 0, len(t.Values))
	for _, v := range t.Values {
		arg, ok := convert(v)
		if !ok {
			message := fmt.Sprintf("%s: %q is not a usable value", t.Field, v)
			if t.Field == "amt" {
				message = fmt.Sprintf("amt: %q is not an amount (e.g. 50 or 50.75)", v)
			}
			return &Diagnostic{Term: t.Raw, Code: badValueCode, Message: message, Position: t.Position}
		}
		clauses = append(clauses, sink.Clause(column+" "+op+" $%d", arg))
	}
	wrapGroup(sink, clauses, negate)
	return nil
}

// wrapGroup appends the rendered clauses as one group: OR-ed normally, or
// wrapped in NOT when the term was negated. An empty group adds nothing, which
// is what makes a wholly-unresolvable term a genuine no-op rather than a false
// predicate — the diagnostic is then the only record that the user asked for
// something that does not exist.
func wrapGroup(sink Sink, clauses []string, negate bool) {
	if len(clauses) == 0 {
		return
	}
	if negate {
		sink.Raw("NOT (" + strings.Join(clauses, " OR ") + ")")
		return
	}
	sink.AnyOf(clauses)
}

// cmpFormat maps a comparison operator onto its SQL symbol. OpLike is absent on
// purpose: it has no meaning on a number or a date.
func cmpFormat(op Op) (string, bool) {
	switch op {
	case OpEq:
		return "=", true
	case OpNe:
		return "<>", true
	case OpGt:
		return ">", true
	case OpGe:
		return ">=", true
	case OpLt:
		return "<", true
	case OpLe:
		return "<=", true
	}
	return "", false
}
