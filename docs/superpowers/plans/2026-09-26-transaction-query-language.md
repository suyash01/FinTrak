# Transaction Query Language Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a power user type one expression (`cat:<id> amt>50 tag:vacation`) into the Transactions search box, evaluated server-side and pushed into the existing `txnFilter`, with a lenient parser that never fails and reports every dropped term in the response.

**Architecture:** A new `backend/internal/query` package tokenizes and parses the `q` parameter, producing a flat AND-ed `Expr` plus a list of diagnostics. A compiler in the same package emits that `Expr` into a `Sink` interface that `handlers.txnFilter` satisfies via a thin adapter, so tenant scope stays the outermost conjunct and no existing pgxmock test changes. The frontend parses with the same grammar, rewrites values (name→id, period→dates), and re-serializes; one JSON corpus pins both implementations.

**Tech Stack:** Go 1.27, Gin, pgx, `pgxmock`; React 19, TypeScript, Vite, Vitest + jsdom, cmdk.

**Spec:** `docs/superpowers/specs/2026-09-26-transaction-query-language-design.md` — read it before starting; this plan argues from it.

## Global Constraints

- `q` **never** produces a 4xx. Every uncompilable term is dropped and reported.
- All terms are AND-ed. `not` negates one term. There is no `or` and no parentheses in v1.
- OR lives inside a value as CSV: `cat:a,b` is "any of". `not cat:a,b` is "neither a nor b".
- A token that is not `field:value` shaped is **plain search** — the existing 4-way OR over description, notes, payee name and tags.
- `field:` consumes exactly one value (quoted or not). Unquoted values stop at whitespace.
- `amt` is decimal major units in the query, **integer minor units on the wire**. Parse with integer arithmetic, never `parseFloat`.
- Caps: 2000 characters, 32 terms. Past either cap, terms are dropped with `too_long`.
- The compiler emits through `query.Sink`; `handlers.txnFilter` gets a 4-method adapter, not an export.
- Every emitted fragment references only `t.` (or a correlated `EXISTS`), so the list query and the `COUNT(*)` query can never disagree.
- Sorting is **not** in the language. `sortBy`/`sortOrder` stay whitelisted parameters.
- Logging uses `log/slog` with typed attrs; never the bare key/value form.
- Money is never computed in `float64`.
- Backend coverage floor is 85% (`make test-cover-check`); frontend 85% (`make test-client-cover-check` is the client module, frontend is `bun run test:coverage` with its own v8 thresholds in `vitest.config.ts`).

## Review Focus

The spec is a vision document; its silence on an input is not permission to break it. These are the five failure modes most likely to bite a real user, each pinned by a test in the task that owns the code:

1. **`q` that is entirely garbage** (`q=%%%`, or `q=catgory:food`) must return **200 with every transaction** plus a visible banner. It must not 400, and it must not look like a working filter — the user asked a question and got the whole ledger back.
2. **`amt>50` must mean fifty dollars, not fifty cents.** A query for $50 must not match a 0.50 transaction. The major→minor conversion is the whole point and is invisible in the SQL.
3. **`q` AND-ed with a filter-bar selection must intersect, not replace.** Selecting Groceries in the multi-select and typing `amt>100` must show only large grocery transactions, not one set or the other.
4. **A name matching two payees must both filter (as an OR) *and* report the ambiguity** — not silently pick one, and not silently widen to all payees.
5. **CSV export with `q` must return the same rows the list total claims.** Both go through `txnQueryFilter`, so a compiler that emitted a predicate referencing a joined table would silently break the export's `COUNT(*)` guard (`transaction_export.go` refuses rather than truncates past 100 000 rows).

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/query/parse.go` | Tokenizer + lenient parser. Never errors. → `Expr`, `[]Diagnostic` |
| `backend/internal/query/fields.go` | The one field table: name → legal ops, enum, value validator |
| `backend/internal/query/compile.go` | `Expr` → SQL clauses into a `Sink` |
| `backend/internal/query/pattern.go` | `EscapeLikePattern`, moved out of `handlers` |
| `backend/internal/query/testdata/corpus.json` | The shared grammar contract, read by Go **and** vitest |
| `backend/handlers/transaction.go` | Read `c.Query("q")`, compile, attach diagnostics to the response |
| `backend/handlers/query_sink.go` | The 4-method `txnFilter` → `query.Sink` adapter |
| `frontend/src/lib/query/parse.ts` | The grammar, in TypeScript |
| `frontend/src/lib/query/fields.ts` | The field table, mirroring `fields.go` |
| `frontend/src/lib/query/resolve.ts` | name→id, period→dates, ambiguity detection |
| `frontend/src/lib/query/serialize.ts` | `Expr` → the `q=` string |
| `frontend/src/lib/query/useQueryLanguage.ts` | Text → debounce → resolve → send → merged banner |
| `frontend/src/lib/query/QueryInput.tsx` | The input, the autocomplete, the grammar sheet |
| `frontend/src/lib/query/QueryDiagnostics.tsx` | The banner |
| `frontend/src/components/Transactions/TransactionFilters.tsx` | Swap the `search` input, render the banner |

---

### Task 1: Grammar and the lenient parser

**Files:**
- Create: `backend/internal/query/parse.go`
- Create: `backend/internal/query/testdata/corpus.json`
- Test: `backend/internal/query/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```go
  type Op string
  const (OpEq Op = "="; OpNe Op = "!="; OpGt Op = ">"; OpGe Op = ">="
          OpLt Op = "<"; OpLe Op = "<="; OpLike Op = "~")

  const FieldPlain = "plain"   // synthesised by the parser, never user-typed

  type Term struct {
      Field    string
      Op       Op
      Values   []string   // CSV items; a plain term holds all its words here
      Negated  bool
      Position int        // byte offset in the raw query
      Raw      string     // the source text, for diagnostics
  }

  type Expr struct{ Terms []Term }

  type Diagnostic struct {
      Term     string `json:"term"`
      Code     string `json:"code"`
      Message  string `json:"message"`
      Position int    `json:"position"`
  }

  // Parse never returns an error. Unparseable terms are dropped and reported.
  func Parse(q string) (Expr, []Diagnostic)
  ```

- [ ] **Step 1: Write the failing parser test**

Create `backend/internal/query/parse_test.go`:

```go
package query

import "testing"

func TestParseAcceptsTheDocumentedForms(t *testing.T) {
	cases := []struct {
		in    string
		terms []Term
	}{
		{"cat:8a3f", []Term{{Field: "cat", Op: OpEq, Values: []string{"8a3f"}}}},
		{"amt>50", []Term{{Field: "amt", Op: OpGt, Values: []string{"50"}}}},
		{"amt>=50", []Term{{Field: "amt", Op: OpGe, Values: []string{"50"}}}},
		{"not cat:a,b", []Term{{Field: "cat", Op: OpEq, Values: []string{"a", "b"}, Negated: true}}},
		{`payee:"Whole Foods"`, []Term{{Field: "payee", Op: OpEq, Values: []string{"Whole Foods"}}}},
		{"coffee", []Term{{Field: FieldPlain, Op: OpLike, Values: []string{"coffee"}}}},
		{"coffee shop", []Term{{Field: FieldPlain, Op: OpLike, Values: []string{"coffee", "shop"}}}},
		{"desc:coffee shop", []Term{
			{Field: "desc", Op: OpEq, Values: []string{"coffee"}},
			{Field: FieldPlain, Op: OpLike, Values: []string{"shop"}},
		}},
		{"cat:a desc:rent", []Term{
			{Field: "cat", Op: OpEq, Values: []string{"a"}},
			{Field: "desc", Op: OpEq, Values: []string{"rent"}},
		}},
	}
	for _, tc := range cases {
		expr, diags := Parse(tc.in)
		if len(diags) != 0 {
			t.Errorf("Parse(%q) diagnostics = %+v, want none", tc.in, diags)
		}
		if len(expr.Terms) != len(tc.terms) {
			t.Errorf("Parse(%q) got %d terms, want %d (%+v)", tc.in, len(expr.Terms), len(tc.terms), expr.Terms)
			continue
		}
		for i, want := range tc.terms {
			got := expr.Terms[i]
			if got.Field != want.Field || got.Op != want.Op || got.Negated != want.Negated {
				t.Errorf("Parse(%q) term %d = %+v, want field=%q op=%q neg=%v",
					tc.in, i, got, want.Field, want.Op, want.Negated)
			}
			if len(got.Values) != len(want.Values) {
				t.Errorf("Parse(%q) term %d values = %v, want %v", tc.in, i, got.Values, want.Values)
				continue
			}
			for j := range want.Values {
				if got.Values[j] != want.Values[j] {
					t.Errorf("Parse(%q) term %d value %d = %q, want %q", tc.in, i, j, got.Values[j], want.Values[j])
				}
			}
		}
	}
}

func TestParseDropsAndReportsInsteadOfFailing(t *testing.T) {
	expr, diags := Parse("catgory:food amt>>50 cat:8a3f")
	if len(expr.Terms) != 1 || expr.Terms[0].Field != "cat" {
		t.Fatalf("expected only the valid term to survive, got %+v", expr.Terms)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %+v", len(diags), diags)
	}
	if diags[0].Code != "unknown_field" || diags[0].Term != "catgory:food" {
		t.Errorf("first diagnostic = %+v, want unknown_field for catgory:food", diags[0])
	}
	if diags[1].Code != "bad_operator" {
		t.Errorf("second diagnostic = %+v, want bad_operator", diags[1])
	}
}

func TestParseReportsMissingValue(t *testing.T) {
	_, diags := Parse("cat:")
	if len(diags) != 1 || diags[0].Code != "missing_value" {
		t.Fatalf("got %+v, want one missing_value diagnostic", diags)
	}
}

func TestParseEnforcesCaps(t *testing.T) {
	long := ""
	for i := 0; i < 40; i++ {
		long += "desc:a"
	}
	expr, diags := Parse(long)
	if len(expr.Terms) != MaxTerms {
		t.Errorf("got %d terms, want the cap of %d", len(expr.Terms), MaxTerms)
	}
	found := false
	for _, d := range diags {
		if d.Code == "too_long" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a too_long diagnostic, got %+v", diags)
	}
}

func TestParseQuotedValueKeepsCommas(t *testing.T) {
	expr, diags := Parse(`tag:"food, drink"`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if len(expr.Terms[0].Values) != 1 || expr.Terms[0].Values[0] != "food, drink" {
		t.Fatalf("got %v, want the comma kept inside the quotes", expr.Terms[0].Values)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend; go test ./internal/query/`
Expected: FAIL — `undefined: Parse`, `undefined: Term`, `undefined: MaxTerms`.

- [ ] **Step 3: Write the parser**

Create `backend/internal/query/parse.go`:

```go
// Package query parses the `q` parameter of GET /transactions: a small
// expression language a power user can type into the transaction search box.
//
// The parser is lenient by design. Nothing here returns an error: a term it
// cannot understand is dropped and reported, because a dropped constraint
// silently widens a result set and the user would read the whole ledger as if
// it were the answer. Every dropped term therefore comes back as a Diagnostic,
// and the handler puts those in the response so the UI can say what it ignored.
package query

import "strings"

// Caps on a single query. The compiler emits one SQL fragment and one bound
// argument per term, so these bound the statement one request can produce.
const (
	MaxQueryChars = 2000
	MaxTerms      = 32
)

// Op is a comparison operator. OpLike ("~") is a case-insensitive contains.
type Op string

const (
	OpEq   Op = "="
	OpNe   Op = "!="
	OpGt   Op = ">"
	OpGe   Op = ">="
	OpLt   Op = "<"
	OpLe   Op = "<="
	OpLike Op = "~"
)

// FieldPlain is the pseudo-field a bare word compiles to: the existing 4-way
// OR over description, notes, payee name and tags. The parser synthesises it
// and never accepts it as user input, so `plain:coffee` is an unknown field.
const FieldPlain = "plain"

// Term is one parsed term. Every term in an Expr is AND-ed with the others.
type Term struct {
	Field    string
	Op       Op
	Values   []string
	Negated  bool
	Position int
	Raw      string
}

// Expr is a flat, AND-ed list of terms. V1 has no boolean tree: the OR a user
// wants lives inside a value as CSV.
type Expr struct{ Terms []Term }

// Diagnostic reports one dropped (or ambiguous) term. Term is the source text
// and Position its byte offset, so the UI can quote the query back verbatim.
type Diagnostic struct {
	Term     string `json:"term"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Position int    `json:"position"`
}

// Diagnostic codes.
const (
	CodeUnknownField   = "unknown_field"
	CodeBadOperator    = "bad_operator"
	CodeMissingValue   = "missing_value"
	CodeUnresolved     = "unresolved_value"
	CodeMalformedAmt   = "malformed_amount"
	CodeMalformedDate  = "malformed_date"
	CodeAmbiguousValue = "ambiguous_value"
	CodeTooLong        = "too_long"
)

// ops is the operator set, longest first so ">=" wins over ">".
var ops = []struct {
	sym string
	op  Op
}{
	{">=", OpGe}, {"<=", OpLe}, {"!=", OpNe}, {"~", OpLike},
	{">", OpGt}, {"<", OpLt}, {"=", OpEq},
}

// Parse tokenizes q and returns the terms it understood plus a diagnostic for
// every term it dropped. It never returns an error: an empty result with a full
// set of diagnostics is a valid, and expected, outcome.
func Parse(q string) (Expr, []Diagnostic) {
	var expr Expr
	var diags []Diagnostic

	if len(q) > MaxQueryChars {
		diags = append(diags, Diagnostic{
			Term: q[:MaxQueryChars], Code: CodeTooLong,
			Message: "query is longer than 2000 characters; the rest was ignored",
		})
		q = q[:MaxQueryChars]
	}

	runes := []rune(q)
	i, n := 0, len(runes)
	skipSpaces := func() {
		for i < n && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
	}

	for {
		skipSpaces()
		if i >= n {
			return expr, diags
		}
		start := i

		if len(expr.Terms) >= MaxTerms {
			diags = append(diags, Diagnostic{
				Term: strings.TrimSpace(string(runes[start:])), Code: CodeTooLong,
				Message: "query has more than 32 terms; the rest was ignored",
			})
			return expr, diags
		}

		// `not` prefix.
		negated := false
		if word, next := readWord(runes, i); strings.EqualFold(word, "not") {
			negated = true
			i = next
			skipSpaces()
			start = i
		}

		// A `field:` prefix, or a bare word.
		word, after := readWord(runes, i)
		if after < n && runes[after] == ':' && word != "" {
			field := strings.ToLower(word)
			def, known := userField(field)
			i = after + 1
			skipSpaces()
			if i >= n {
				diags = append(diags, Diagnostic{
					Term: string(runes[start:after+1]), Code: CodeMissingValue,
					Message: field + ": needs a value", Position: start,
				})
				continue
			}
			raw, values := readValue(runes, i)
			i = raw
			term := Term{Field: field, Op: OpEq, Values: values, Negated: negated, Position: start}
			if !known {
				diags = append(diags, Diagnostic{
					Term: string(runes[start:raw]), Code: CodeUnknownField,
					Message: "unknown field " + field, Position: start,
				})
				continue
			}
			if err := def.validate(term); err != nil {
				diags = append(diags, err.toDiagnostic(string(runes[start:raw]), start))
				continue
			}
			term.Raw = string(runes[start:raw])
			expr.Terms = append(expr.Terms, term)
			continue
		}

		// An operator form, e.g. `amt>50`. The operator sits just past the
		// field name, which is where readWord stopped. Matching at i would
		// never fire, because i is the start of the name, not the operator.
		if op, width, ok := matchOp(runes, after); ok {
			name := strings.ToLower(word)
			def, known := userField(name)
			afterOp := after + width
			j := afterOp
			for j < n && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			if j >= n {
				diags = append(diags, Diagnostic{
					Term: string(runes[start:afterOp]), Code: CodeMissingValue,
					Message: name + " needs a value", Position: start,
				})
				i = afterOp
				continue
			}
			end, values := readValue(runes, j)
			raw := string(runes[start:end])
			switch {
			case !known:
				diags = append(diags, Diagnostic{
					Term: raw, Code: CodeUnknownField,
					Message: "unknown field " + name, Position: start,
				})
			case !def.allowsOp(op):
				diags = append(diags, Diagnostic{
					Term: raw, Code: CodeBadOperator,
					Message: name + " does not support " + string(op), Position: start,
				})
			default:
				term := Term{Field: name, Op: op, Values: values, Negated: negated, Position: start, Raw: raw}
				if err := def.validate(term); err != nil {
					diags = append(diags, err.toDiagnostic(raw, start))
				} else {
					expr.Terms = append(expr.Terms, term)
				}
			}
			i = end
			continue
		}

		// A bare word: plain search. Consume words until the next `field:` or
		// `field<op>` shape, so "coffee shop" is one term.
		var words []string
		j := i
		for j < n {
			w, next := readWord(runes, j)
			if w == "" {
				break
			}
			if next > j {
				words = append(words, w)
				j = next
				continue
			}
			_, width, isOp := matchOp(runes, j)
			if isOp && width > 0 {
				break
			}
			break
		}
		if len(words) == 0 {
			// A character we cannot start a token with; skip it so one stray
			// byte cannot loop forever.
			i++
			continue
		}
		expr.Terms = append(expr.Terms, Term{
			Field: FieldPlain, Op: OpLike, Values: words, Negated: negated,
			Position: start, Raw: strings.Join(words, " "),
		})
		i = j
	}
}

// readWord reads a run of non-space, non-operator characters starting at i,
// returning the word and the index just past it. ok is false when i is at a
// space or the run is empty.
func readWord(runes []rune, i int) (word string, next int) {
	n := len(runes)
	j := i
	for j < n {
		r := runes[j]
		if r == ' ' || r == '\t' || r == '"' || r == ',' {
			break
		}
		if r == '>' || r == '<' || r == '=' || r == '!' || r == '~' {
			// An operator may follow a name (`amt>50`) but not sit inside one.
			if j > i {
				break
			}
			break
		}
		j++
	}
	return string(runes[i:j]), j
}

// matchOp reports the operator starting at i, if any, and its width.
func matchOp(runes []rune, i int) (Op, int, bool) {
	rest := string(runes[i:])
	for _, cand := range ops {
		if strings.HasPrefix(rest, cand.sym) {
			return cand.op, len([]rune(cand.sym)), true
		}
	}
	return "", 0, false
}

// readValue reads one value at i — quoted, or a bare CSV run — and returns the
// index just past it. A quoted value is a single item even if it contains a
// comma; a bare value splits on commas and drops empty items.
func readValue(runes []rune, i int) (end int, values []string) {
	n := len(runes)
	if i < n && runes[i] == '"' {
		var sb strings.Builder
		j := i + 1
		for j < n {
			if runes[j] == '\\' && j+1 < n {
				sb.WriteRune(runes[j+1])
				j += 2
				continue
			}
			if runes[j] == '"' {
				j++
				break
			}
			sb.WriteRune(runes[j])
			j++
		}
		return j, []string{sb.String()}
	}
	j := i
	for j < n && runes[j] != ' ' && runes[j] != '\t' && runes[j] != '"' {
		j++
	}
	for _, part := range strings.Split(string(runes[i:j]), ",") {
		if p := strings.TrimSpace(part); p != "" {
			values = append(values, p)
		}
	}
	if len(values) == 0 {
		// A value of only commas: treat the run as one literal item so the
		// term is reported as unresolvable rather than silently empty.
		return j, []string{string(runes[i:j])}
	}
	return j, values
}
```

- [ ] **Step 4: Write `fields.go`, which the parser calls**

Create `backend/internal/query/fields.go`:

```go
package query

import (
	"fmt"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
)

// fieldDef is the single definition of one field. The parser, the compiler and
// the frontend's suggester all read this table, so a field cannot be suggested
// that the parser will reject.
type fieldDef struct {
	// userTyped is false for FieldPlain, which the parser synthesises. A user
	// typing `plain:coffee` gets unknown_field.
	userTyped bool
	// ops is the legal operator set; nil means every operator.
	ops []Op
	// enum is the fixed value domain, if the field has one.
	enum []string
	// kind selects the value validator.
	kind valueKind
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

var fieldTable = map[string]fieldDef{
	FieldPlain: {kind: kindText},
	"desc":     {userTyped: true, kind: kindText, ops: []Op{OpEq, OpNe, OpLike}},
	"note":     {userTyped: true, kind: kindText, ops: []Op{OpEq, OpNe, OpLike}},
	"cat":      {userTyped: true, kind: kindUUID, ops: []Op{OpEq, OpNe}},
	"group":    {userTyped: true, kind: kindUUID, ops: []Op{OpEq, OpNe}},
	"acct":     {userTyped: true, kind: kindUUID, ops: []Op{OpEq, OpNe}},
	"payee":    {userTyped: true, kind: kindUUID, ops: []Op{OpEq, OpNe}},
	"tag":      {userTyped: true, kind: kindTagList, ops: []Op{OpEq, OpNe}},
	"type":     {userTyped: true, kind: kindEnum, enum: []string{"debit", "credit"}, ops: []Op{OpEq, OpNe}},
	"linked":   {userTyped: true, kind: kindEnum, enum: []string{"true", "false"}, ops: []Op{OpEq, OpNe}},
	"recurring": {userTyped: true, kind: kindEnum, enum: []string{"linked", "unlinked"}, ops: []Op{OpEq, OpNe}},
	"amt":      {userTyped: true, kind: kindAmount, ops: []Op{OpEq, OpNe, OpGt, OpGe, OpLt, OpLe}},
	"date":     {userTyped: true, kind: kindDate, ops: []Op{OpEq, OpNe, OpGt, OpGe, OpLt, OpLe}},
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
	if d.ops == nil {
		return true
	}
	for _, allowed := range d.ops {
		if allowed == op {
			return true
		}
	}
	return false
}

// fieldError is a validation failure carrying the diagnostic code it should
// surface as, so Parse never has to classify errors itself.
type fieldError struct {
	code    string
	message string
}

func (e *fieldError) toDiagnostic(term string, position int) Diagnostic {
	return Diagnostic{Term: term, Code: e.code, Message: e.message, Position: position}
}

func errf(code, format string, args ...any) *fieldError {
	return &fieldError{code: code, message: fmt.Sprintf(format, args...)}
}

// validate checks one term's values against the field's kind. It returns a
// *fieldError, or nil.
func (d fieldDef) validate(t Term) error {
	if len(t.Values) == 0 {
		return errf(CodeMissingValue, "%s: needs a value", t.Field)
	}
	// A multi-value term is an OR, and only equality and negation make sense
	// over a set; the ops table already enforces that.
	for _, v := range t.Values {
		switch d.kind {
		case kindText:
			// Anything is text.
		case kindUUID:
			if v == "none" || v == "uncategorized" {
				continue // the sentinels the existing filters already use
			}
			if !isUUID(v) {
				return errf(CodeUnresolved, "%s: %q is not an id this server can resolve; ids are uuid, \"none\" or \"uncategorized\"", t.Field, v)
			}
		case kindEnum:
			if !contains(d.enum, v) {
				return errf(CodeUnresolved, "%s: %q is not one of %v", t.Field, v, d.enum)
			}
		case kindAmount:
			if _, err := money.Parse(v); err != nil {
				return errf(CodeMalformedAmt, "%s: %q is not an amount (e.g. 50 or 50.75)", t.Field, v)
			}
		case kindDate:
			if _, msg := validation.CheckTransactionDate(v, time.Now()); msg != "" {
				return errf(CodeMalformedDate, "%s: %q is not a usable date (%s)", t.Field, v, msg)
			}
		case kindTagList:
			// Tags are free text; there is no tag table to resolve against.
		}
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
```

Add the `isUUID` helper to `fields.go` (the handlers package has its own unexported copy; do **not** import across the boundary):

```go
func isUUID(v string) bool {
	_, err := uuid.Parse(v)
	return err == nil
}
```

and add `"github.com/google/uuid"` to the import block.

- [ ] **Step 5: Run the parser tests**

Run: `cd backend; go test ./internal/query/`
Expected: PASS. If `TestParseDropsAndReportsInsteadOfFailing` fails on the `bad_operator` code, `matchOp` is matching `>` before `>>` is seen — check that `readWord` stops at the first `>` so `amt>>50` yields field `amt` and the operator `>>` fails `allowsOp`.

- [ ] **Step 6: Create the shared corpus**

Create `backend/internal/query/testdata/corpus.json`. This file is the drift guard: Go reads it here, and the frontend vitest suite reads the same file in Task 6. The `sql` key is read only by Go.

```json
{
  "cases": [
    {
      "name": "single field equality",
      "surface": "cat:8a3f",
      "canonical": "cat:8a3f",
      "terms": [{ "field": "cat", "op": "=", "values": ["8a3f"], "negated": false, "position": 0 }],
      "diagnostics": [],
      "sql": { "clauses": ["t.category_id = $2"], "args": ["8a3f"] }
    },
    {
      "name": "amount greater than",
      "surface": "amt>50",
      "canonical": "amt>=5000",
      "terms": [{ "field": "amt", "op": ">", "values": ["50"], "negated": false, "position": 0 }],
      "diagnostics": [],
      "sql": { "clauses": ["t.amount > $2"], "args": [5000] }
    },
    {
      "name": "csv is an or set",
      "surface": "cat:a,b,c",
      "canonical": "cat:a,b,c",
      "terms": [{ "field": "cat", "op": "=", "values": ["a", "b", "c"], "negated": false, "position": 0 }],
      "diagnostics": []
    },
    {
      "name": "not binds to the whole term",
      "surface": "not cat:a,b",
      "canonical": "not cat:a,b",
      "terms": [{ "field": "cat", "op": "=", "values": ["a", "b"], "negated": true, "position": 0 }],
      "diagnostics": []
    },
    {
      "name": "quoted value keeps its space",
      "surface": "payee:\"Whole Foods\"",
      "canonical": "payee:\"Whole Foods\"",
      "terms": [{ "field": "payee", "op": "=", "values": ["Whole Foods"], "negated": false, "position": 0 }],
      "diagnostics": []
    },
    {
      "name": "bare word is plain search",
      "surface": "coffee",
      "canonical": "coffee",
      "terms": [{ "field": "plain", "op": "~", "values": ["coffee"], "negated": false, "position": 0 }],
      "diagnostics": [],
      "sql": { "kind": "plain" }
    },
    {
      "name": "field prefix consumes one value only",
      "surface": "desc:coffee shop",
      "canonical": "desc:coffee shop",
      "terms": [
        { "field": "desc", "op": "=", "values": ["coffee"], "negated": false, "position": 0 },
        { "field": "plain", "op": "~", "values": ["shop"], "negated": false, "position": 11 }
      ],
      "diagnostics": []
    },
    {
      "name": "two fields are anded",
      "surface": "cat:8a3f amt>=50",
      "canonical": "cat:8a3f amt>=5000",
      "terms": [
        { "field": "cat", "op": "=", "values": ["8a3f"], "negated": false, "position": 0 },
        { "field": "amt", "op": ">=", "values": ["50"], "negated": false, "position": 10 }
      ],
      "diagnostics": []
    },
    {
      "name": "unknown field is dropped and reported",
      "surface": "catgory:food",
      "canonical": "",
      "terms": [],
      "diagnostics": [
        { "term": "catgory:food", "code": "unknown_field", "message": "unknown field catgory", "position": 0 }
      ]
    },
    {
      "name": "bad operator is dropped and reported",
      "surface": "amt>>50",
      "canonical": "",
      "terms": [],
      "diagnostics": [
        { "term": "amt>>50", "code": "bad_operator", "message": "amt does not support >", "position": 0 }
      ]
    },
    {
      "name": "malformed amount is dropped and reported",
      "surface": "amt:5.",
      "canonical": "",
      "terms": [],
      "diagnostics": [
        { "term": "amt:5.", "code": "malformed_amount", "message": "amt: \"5.\" is not an amount (e.g. 50 or 50.75)", "position": 0 }
      ]
    },
    {
      "name": "out of window date is dropped and reported",
      "surface": "date:0001-01-01",
      "canonical": "",
      "terms": [],
      "diagnostics": [
        { "term": "date:0001-01-01", "code": "malformed_date", "message": "date: \"0001-01-01\" is not a usable date (must be between 1900-01-01 and one year from today)", "position": 0 }
      ]
    },
    {
      "name": "bad enum value is dropped and reported",
      "surface": "type:money",
      "canonical": "",
      "terms": [],
      "diagnostics": [
        { "term": "type:money", "code": "unresolved_value", "message": "type: \"money\" is not one of [debit credit]", "position": 0 }
      ]
    },
    {
      "name": "everything invalid yields no terms",
      "surface": "%%%",
      "canonical": "",
      "terms": [],
      "diagnostics": []
    }
  ]
}
```

- [ ] **Step 7: Write the corpus-driven parser test**

Append to `backend/internal/query/parse_test.go`:

```go
type corpusCase struct {
	Name        string       `json:"name"`
	Surface     string       `json:"surface"`
	Canonical   string       `json:"canonical"`
	Terms       []Term       `json:"terms"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// TestCorpusGrammarContract runs the shared corpus. The frontend's vitest
// suite reads this same file (frontend/src/lib/query/parse.test.ts), so a field
// or operator added on one side without the other fails both suites.
func TestCorpusGrammarContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus struct{ Cases []corpusCase `json:"cases"` }
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus is empty; it is the cross-language drift guard")
	}
	for _, tc := range corpus.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			expr, diags := Parse(tc.Surface)
			if len(expr.Terms) != len(tc.Terms) {
				t.Fatalf("Parse(%q) got %d terms, want %d (%+v)", tc.Surface, len(expr.Terms), len(tc.Terms), expr.Terms)
			}
			for i, want := range tc.Terms {
				got := expr.Terms[i]
				if got.Field != want.Field || got.Op != want.Op || got.Negated != want.Negated || got.Position != want.Position {
					t.Errorf("term %d = %+v, want %+v", i, got, want)
				}
				if strings.Join(got.Values, ",") != strings.Join(want.Values, ",") {
					t.Errorf("term %d values = %v, want %v", i, got.Values, want.Values)
				}
			}
			if len(diags) != len(tc.Diagnostics) {
				t.Fatalf("Parse(%q) got %d diagnostics, want %d (%+v)", tc.Surface, len(diags), len(tc.Diagnostics), diags)
			}
			for i, want := range tc.Diagnostics {
				if diags[i].Code != want.Code || diags[i].Term != want.Term || diags[i].Position != want.Position {
					t.Errorf("diagnostic %d = %+v, want %+v", i, diags[i], want)
				}
			}
		})
	}
}
```

Add `"encoding/json"`, `"os"` and `"strings"` to the test file's imports.

- [ ] **Step 8: Run the corpus test and fix the expectations**

Run: `cd backend; go test ./internal/query/`
Expected: FAIL on the first mismatched message string. The diagnostics in the corpus were written by hand; correct each corpus entry to the message the parser actually produces, and change the parser only if its message is the worse one. Do not weaken a corpus assertion to make a test pass.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/query
git commit -m "feat(query): lenient parser and shared grammar corpus"
```

---

### Task 2: Compile an Expr into SQL

**Files:**
- Create: `backend/internal/query/compile.go`
- Create: `backend/internal/query/pattern.go`
- Test: `backend/internal/query/compile_test.go`

**Interfaces:**
- Consumes: `Parse`, `Term`, `Expr`, `Diagnostic`, `fieldTable` from Task 1.
- Produces:
  ```go
  // Sink is what the compiler appends predicates to. *handlers.txnFilter
  // satisfies it through an adapter; see handlers/query_sink.go.
  type Sink interface {
      Param(clause string, value any)          // one bound placeholder
      Clause(format string, value any) string  // renders without adding
      AnyOf(clauses []string)                   // OR the rendered clauses
      Params(clause string, values ...any)      // several bound placeholders
  }

  // Compile appends every term's predicate to sink. It returns a diagnostic for
  // any term it could not emit, so a compile failure is as visible as a parse
  // failure. Callers must have already appended their own clauses, because this
  // appends after them and the placeholder numbering depends on that order.
  func Compile(e Expr, sink Sink) []Diagnostic
  ```

- [ ] **Step 1: Write the failing compile test**

Create `backend/internal/query/compile_test.go`:

```go
package query

import (
	"reflect"
	"testing"
)

// recSink records what the compiler emitted, standing in for txnFilter.
type recSink struct {
	clauses []string
	args    []any
	// start mirrors txnFilter's args[0] = userID, so placeholders begin at $2.
	next int
}

func newRecSink() *recSink { return &recSink{next: 1} }

func (s *recSink) Param(clause string, value any) {
	s.args = append(s.args, value)
	s.clauses = append(s.clauses, sprintf(clause, len(s.args)))
}
func (s *recSink) Clause(format string, value any) string {
	s.args = append(s.args, value)
	return sprintf(format, len(s.args))
}
func (s *recSink) AnyOf(clauses []string) {
	if len(clauses) == 0 {
		return
	}
	s.clauses = append(s.clauses, "("+join(clauses, " OR ")+")")
}
func (s *recSink) Params(clause string, values ...any) {
	first := len(s.args) + 1
	s.args = append(s.args, values...)
	ph := make([]any, len(values))
	for i := range values {
		ph[i] = first + i
	}
	s.clauses = append(s.clauses, sprintf(clause, ph...))
}

// TestCompileMajorUnitsBecomeMinorUnits is the money test: `amt>50` is fifty
// dollars and must bind 5000, never 50. A query for $50 that matched a $0.50
// transaction would be silently, invisibly wrong.
func TestCompileMajorUnitsBecomeMinorUnits(t *testing.T) {
	expr, diags := Parse("amt>50")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	sink := newRecSink()
	if out := Compile(expr, sink); len(out) != 0 {
		t.Fatalf("unexpected compile diagnostics: %+v", out)
	}
	if !reflect.DeepEqual(sink.args, []any{int64(5000)}) {
		t.Errorf("args = %#v, want []any{int64(5000)}", sink.args)
	}
	if sink.clauses[0] != "t.amount > $2" {
		t.Errorf("clause = %q, want %q", sink.clauses[0], "t.amount > $2")
	}
}

// TestCompileFractionalAmountIsExact guards the other half: 50.75 is 5075, and
// no float ever touches the value.
func TestCompileFractionalAmountIsExact(t *testing.T) {
	expr, _ := Parse("amt>=50.75")
	sink := newRecSink()
	Compile(expr, sink)
	if !reflect.DeepEqual(sink.args, []any{int64(5075)}) {
		t.Errorf("args = %#v, want []any{int64(5075)}", sink.args)
	}
}

func TestCompileCSVIsOredAndNegationWrapsTheGroup(t *testing.T) {
	expr, _ := Parse("not cat:a,b")
	sink := newRecSink()
	Compile(expr, sink)
	if len(sink.clauses) != 1 {
		t.Fatalf("clauses = %v, want one OR group", sink.clauses)
	}
	if sink.clauses[0] != "NOT ((t.category_id = $2 OR t.category_id = $3))" {
		t.Errorf("clause = %q", sink.clauses[0])
	}
}

func TestCompilePlainSearchIsTheExistingFourWayOr(t *testing.T) {
	expr, _ := Parse("coffee")
	sink := newRecSink()
	Compile(expr, sink)
	if len(sink.clauses) != 1 {
		t.Fatalf("clauses = %v, want one clause", sink.clauses)
	}
	for _, want := range []string{"LOWER(t.description) LIKE LOWER($2)", "LOWER(COALESCE(t.notes, '')) LIKE LOWER($3)", "payees sp", "unnest(t.tags)"} {
		if !contains2(sink.clauses[0], want) {
			t.Errorf("clause %q is missing %q", sink.clauses[0], want)
		}
	}
}

func TestCompileEscapesLikeWildcards(t *testing.T) {
	expr, _ := Parse(`desc~100%`)
	sink := newRecSink()
	Compile(expr, sink)
	if sink.args[0] != "%100\\%%" {
		t.Errorf("arg = %#v, want the percent escaped so it matches literally", sink.args[0])
	}
}

// TestCompileOnlyReferencesTheTransactionsTable is the invariant that keeps the
// list query and the COUNT(*) query in agreement: txnQueryFilter runs the same
// predicate with no joins, so a fragment naming a joined table would make the
// count 500 while the page renders.
func TestCompileOnlyReferencesTheTransactionsTable(t *testing.T) {
	queries := []string{
		"coffee", "desc:rent", "note:memo", "cat:none", "cat:a,b",
		"group:8a3f", "acct:8a3f", "payee:none", "payee:8a3f",
		"tag:food", "type:debit", "amt>50", "date>=2026-01-01",
		"linked:true", "recurring:unlinked",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, diags := Parse(q)
			if len(diags) != 0 {
				t.Skipf("query does not parse: %+v", diags)
			}
			sink := newRecSink()
			Compile(expr, sink)
			for _, c := range sink.clauses {
				if contains2(c, " JOIN ") {
					t.Errorf("clause %q contains a JOIN; the count query has none", c)
				}
			}
		})
	}
}
```

Add the two test helpers at the bottom of the file:

```go
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
func join(parts []string, sep string) string   { return strings.Join(parts, sep) }
func contains2(haystack, needle string) bool   { return strings.Contains(haystack, needle) }
```

and import `"fmt"` and `"strings"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend; go test ./internal/query/`
Expected: FAIL — `undefined: Compile`, `undefined: Sink`.

- [ ] **Step 3: Move `escapeLikePattern` out of handlers**

Create `backend/internal/query/pattern.go`:

```go
package query

import "strings"

// EscapeLikePattern escapes the LIKE metacharacters in user text so a term like
// `desc~100%` matches the literal text "100%" rather than every string starting
// with "100". It is the same escaping the rules engine's matchRule applies
// (backend/handlers/rule.go), moved here so the query compiler and the rules
// engine cannot drift.
func EscapeLikePattern(p string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(p)
}
```

Then in `backend/handlers/rule.go`, delete the local `escapeLikePattern`
function body and replace it with an alias, so every existing call site and test
keeps compiling unchanged:

```go
// escapeLikePattern is the query package's LIKE escaping, aliased so the rules
// engine and the q= query language cannot drift apart.
var escapeLikePattern = query.EscapeLikePattern
```

Add `"github.com/fintrak/backend/internal/query"` to `rule.go`'s imports. Run
`cd backend; go build ./...` to confirm.

- [ ] **Step 4: Write the compiler**

Create `backend/internal/query/compile.go`:

```go
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
	// `not x` and `x!=v` are the same request, so both spellings negate. This
	// is why no emitter below has to look at Op for the ne case.
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
		return emitOrdered(t, sink, "t.amount", negate, func(v string) (any, string) {
			amount, err := money.Parse(v)
			if err != nil {
				return nil, ""
			}
			return amount.Cents(), ""
		}, CodeMalformedAmt)
	case "date":
		return emitOrdered(t, sink, "t.date", negate, func(v string) (any, string) { return v, "" }, "")
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

// emitTags binds the whole CSV as one text[] and uses array overlap, so any of
// the tags matches. Tags are names, not ids: there is no tag table to resolve
// against, which is why the frontend passes tag names straight through.
func emitTags(t Term, sink Sink, negate bool) *Diagnostic {
	if len(t.Values) == 0 {
		return &Diagnostic{Term: t.Raw, Code: CodeMissingValue, Message: "tag: needs a value", Position: t.Position}
	}
	if negate {
		sink.Raw("NOT (t.tags && '" + t.Values[0] + "'::text[])")
		return nil
	}
	sink.Raw("t.tags && '" + t.Values[0] + "'::text[]")
	return nil
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

// emitOrdered is the shared body of the two comparable fields, amt and date.
// convert turns a validated value into the argument to bind; a nil return means
// the value could not be converted and the term is dropped with badValueCode.
func emitOrdered(t Term, sink Sink, column string, negate bool, convert func(string) (any, string), badValueCode string) *Diagnostic {
	format, ok := cmpFormat(t.Op)
	if !ok {
		return &Diagnostic{Term: t.Raw, Code: CodeBadOperator, Message: t.Field + " does not support " + string(t.Op), Position: t.Position}
	}
	clauses := make([]string, 0, len(t.Values))
	for _, v := range t.Values {
		arg, _ := convert(v)
		if arg == nil {
			code := badValueCode
			if code == "" {
				code = CodeMalformedDate
			}
			message := fmt.Sprintf("%s: %q is not a usable value", t.Field, v)
			if t.Field == "amt" {
				message = fmt.Sprintf("amt: %q is not an amount (e.g. 50 or 50.75)", v)
			}
			return &Diagnostic{Term: t.Raw, Code: code, Message: message, Position: t.Position}
		}
		clauses = append(clauses, sink.Clause(column+" "+format+" $%d", arg))
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

// cmpFormat maps a comparison operator onto a SQL fragment with one %d. OpLike
// is absent on purpose: it has no meaning on a number or a date.
func cmpFormat(op Op) (string, bool) {
	switch op {
	case OpEq:
		return "= $%d", true
	case OpNe:
		return "<> $%d", true
	case OpGt:
		return "> $%d", true
	case OpGe:
		return ">= $%d", true
	case OpLt:
		return "< $%d", true
	case OpLe:
		return "<= $%d", true
	}
	return "", false
}
```

Three decisions in that code worth stating, because each is visible in the SQL:

- **`emitTags` binds the tag names into a literal array, not a parameter.** The
  existing `tags` parameter in `txnQueryFilter` binds `$n::text[]`; doing the same
  here would need a `[]string` driver value, which pgx handles but which makes
  the tag list unreadable in a log. Because the whole CSV is one value and the
  names are escaped, the literal form is the one place the compiler emits any
  user text into the statement — so `EscapeLikePattern`'s sibling rule applies:
  reject a tag containing a single quote outright (a diagnostic, not a
  statement). Add that guard:

  ```go
  // in emitTags, before binding
  for _, v := range t.Values {
      if strings.Contains(v, "'") {
          return &Diagnostic{Term: t.Raw, Code: CodeUnresolved, Message: "tag names cannot contain a quote", Position: t.Position}
      }
  }
  ```

- **`wrapGroup` uses `Raw` for the negated case**, so `not cat:a,b` emits one
  clause, `NOT ((t.category_id = $2 OR t.category_id = $3))`, rather than two
  separate NOTs that would not be equivalent for a CSV.

- **`emitOrdered` is shared by `amt` and `date`** because they differ only in the
  conversion, and having one copy is what stops the money conversion from
  drifting between them.

- [ ] **Step 5: Run the compile tests**

Run: `cd backend; go test ./internal/query/`
Expected: PASS. The negated-CSV clause is
`NOT ((t.category_id = $2 OR t.category_id = $3))` — the outer parens come from
`wrapGroup`, the inner from `strings.Join(clauses, " OR ")`. A single-clause OR
group keeps `AnyOf`'s own parens, so `cat:8a3f` emits `(t.category_id = $2)`.

- [ ] **Step 6: Add the corpus SQL assertions**

Append to `compile_test.go`:

```go
// corpusSQLCase mirrors the `sql` key of the shared corpus. The frontend's
// vitest suite ignores this key; it exists to pin the Go compiler's emitted
// fragment and bound arguments for each documented example.
type corpusSQLCase struct {
	Name    string `json:"name"`
	Surface string `json:"surface"`
	SQL     *struct {
		Clauses []string `json:"clauses"`
		Args    []any    `json:"args"`
		Kind    string   `json:"kind"`
	} `json:"sql"`
}

// readCorpusSQL decodes the corpus with UseNumber, so an integer argument in
// the fixture stays an exact integer instead of becoming a float64 — a float
// round-trip would defeat the point of a test that exists to catch a money
// conversion going wrong.
func readCorpusSQL(t *testing.T) []corpusSQLCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var corpus struct{ Cases []corpusSQLCase `json:"cases"` }
	if err := dec.Decode(&corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus is empty; it is the cross-language drift guard")
	}
	return corpus.Cases
}

// normalizeArg turns a json.Number into the int64 the compiler binds, so the
// fixture can be written in plain JSON.
func normalizeArg(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	i, err := n.Int64()
	if err != nil {
		return v
	}
	return i
}

func TestCorpusSQLContract(t *testing.T) {
	for _, tc := range readCorpusSQL(t) {
		if tc.SQL == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			if tc.SQL.Kind == "plain" {
				// The free-text shape is a four-way group with four placeholders
				// per word, which TestCompilePlainSearchIsTheExistingFourWayOr
				// asserts directly; pinning it in the fixture too would only
				// restate it.
				return
			}
			expr, diags := Parse(tc.Surface)
			if len(diags) != 0 {
				t.Fatalf("Parse(%q) diagnostics: %+v", tc.Surface, diags)
			}
			sink := newRecSink()
			if out := Compile(expr, sink); len(out) != 0 {
				t.Fatalf("Compile(%q) diagnostics: %+v", tc.Surface, out)
			}
			if !reflect.DeepEqual(sink.clauses, tc.SQL.Clauses) {
				t.Errorf("clauses = %v, want %v", sink.clauses, tc.SQL.Clauses)
			}
			want := make([]any, len(tc.SQL.Args))
			for i, a := range tc.SQL.Args {
				want[i] = normalizeArg(a)
			}
			if !reflect.DeepEqual(sink.args, want) {
				t.Errorf("args = %#v, want %#v", sink.args, want)
			}
		})
	}
}
```

Add `"bytes"` to the test file's imports. The corpus needs one more entry so the
`clauses` and `args` keys are both exercised on a case with more than one term —
append to `testdata/corpus.json`:

```json
    {
      "name": "two fields produce two clauses",
      "surface": "cat:8a3f amt>50",
      "canonical": "cat:8a3f amt>=5000",
      "terms": [
        { "field": "cat", "op": "=", "values": ["8a3f"], "negated": false, "position": 0 },
        { "field": "amt", "op": ">", "values": ["50"], "negated": false, "position": 10 }
      ],
      "diagnostics": [],
      "sql": { "clauses": ["(t.category_id = $2)", "t.amount > $3"], "args": ["8a3f", 5000] }
    },
    {
      "name": "not inverts the or group",
      "surface": "not cat:a,b",
      "canonical": "not cat:a,b",
      "terms": [{ "field": "cat", "op": "=", "values": ["a", "b"], "negated": true, "position": 0 }],
      "diagnostics": [],
      "sql": { "clauses": ["NOT ((t.category_id = $2 OR t.category_id = $3))"], "args": ["a", "b"] }
    }
```

The two entries above supersede the earlier `csv is an or set` and
`not binds to the whole term` entries, which carried no `sql` key. Replace those
two with these, so every `sql` case is asserted and every grammar case is
asserted exactly once.

Run: `cd backend; go test ./internal/query/ -run TestCorpusSQL -v`
Expected: PASS. `t.category_id = $2` is wrapped in parentheses by `Sink.AnyOf`
when a single clause is OR-ed, which is why the fixture shows `(t.category_id = $2)`
and not `t.category_id = $2`.


- [ ] **Step 7: Commit**

```bash
git add backend/internal/query backend/handlers/rule.go
git commit -m "feat(query): compile an Expr into parameterized SQL"
```

---

### Task 3: Wire `q` into the handler, the response, and the export

**Files:**
- Create: `backend/handlers/query_sink.go`
- Modify: `backend/handlers/transaction.go` (`txnQueryFilter` ~line 103, `GetTransactions` response ~line 426)
- Test: `backend/handlers/query_test.go`

**Interfaces:**
- Consumes: `query.Parse`, `query.Compile`, `query.Sink` from Tasks 1–2.
- Produces: `GET /transactions?q=...` and `GET /transactions/export?q=...` both accept `q`; the list response gains an optional `queryDiagnostics` array.

- [ ] **Step 1: Write the failing handler test**

Create `backend/handlers/query_test.go`. Follow `testhelpers_test.go`'s
`newTestServer` and the existing `new*TestRouter()` pattern; use
`gin.SetMode(gin.TestMode)`:

```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v4"
)

// newQueryTestRouter wires a router exposing GET /transactions with the shared
// filter, so the q= grammar is exercised through the real handler.
func newQueryTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/transactions", testAuthMiddleware(), srv.GetTransactions)
	return r
}

// TestQueryParamAppendsAfterTheExistingParams is the AND-semantics test:
// q= must not replace the filter-bar's selection, it must intersect with it.
func TestQueryParamAppendsAfterTheExistingParams(t *testing.T) {
	mock, srv := newTestServer(t)
	userID := testUserID

	rows := mock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "created_at", "payee_id", "billing_cycle_id"}).
		AddRow(testUUID(1), testUUID(2), testDate, "Rent", int64(120000), "debit", testUUID(3), []string{}, "", testNow, nil, nil)
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID, testUUID(3)).
		WillReturnRows(mock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, testUUID(3), int64(5000)).
		WillReturnRows(rows)

	w := newQueryTestRouter(srv)
	req := httptest.NewRequest(http.MethodGet, "/transactions?categoryId="+testUUID(3)+"&q=amt%3E50", nil)
	rec := httptest.NewRecorder()
	w.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet SQL expectations: %v", err)
	}
}

// TestQueryOfPureGarbageReturnsEverythingAndSaysSo is Review Focus #1: a query
// the parser cannot understand must return 200 with the full ledger plus a
// diagnostic, never a 400 and never a silently-plausible empty filter.
func TestQueryOfPureGarbageReturnsEverythingAndSaysSo(t *testing.T) {
	mock, srv := newTestServer(t)
	userID := testUserID

	rows := mock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "created_at", "payee_id", "billing_cycle_id"}).
		AddRow(testUUID(1), testUUID(2), testDate, "Coffee", int64(450), "debit", nil, []string{}, "", testNow, nil, nil)
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID).
		WillReturnRows(mock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID).
		WillReturnRows(rows)

	w := newQueryTestRouter(srv)
	req := httptest.NewRequest(http.MethodGet, "/transactions?q=catgory%3Afood", nil)
	rec := httptest.NewRecorder()
	w.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; a bad q must never fail the request", rec.Code)
	}
	var body struct {
		Total             int                `json:"total"`
		QueryDiagnostics []query.Diagnostic `json:"queryDiagnostics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 1 {
		t.Errorf("total = %d, want 1 (a dropped term must not filter)", body.Total)
	}
	if len(body.QueryDiagnostics) != 1 {
		t.Fatalf("queryDiagnostics = %+v, want exactly one", body.QueryDiagnostics)
	}
	if body.QueryDiagnostics[0].Code != "unknown_field" {
		t.Errorf("code = %q, want unknown_field", body.QueryDiagnostics[0].Code)
	}
}

// TestQueryDiagnosticsOmittedWhenEmpty keeps the key off the wire entirely
// rather than sending an empty array, so a caller can distinguish "nothing was
// ignored" without a null check.
func TestQueryDiagnosticsOmittedWhenEmpty(t *testing.T) {
	mock, srv := newTestServer(t)
	userID := testUserID
	rows := mock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "created_at", "payee_id", "billing_cycle_id"}).
		AddRow(testUUID(1), testUUID(2), testDate, "Coffee", int64(450), "debit", nil, []string{}, "", testNow, nil, nil)
	mock.ExpectQuery("SELECT COUNT").WithArgs(userID).WillReturnRows(mock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id").WithArgs(userID).WillReturnRows(rows)

	w := newQueryTestRouter(srv)
	req := httptest.NewRequest(http.MethodGet, "/transactions?q=coffee", nil)
	rec := httptest.NewRecorder()
	w.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "queryDiagnostics") {
		t.Errorf("body carries queryDiagnostics with nothing ignored: %s", rec.Body.String())
	}
}
```

Adjust the row/column lists and the `testUUID`/`testDate`/`testNow` helpers to
the ones `transaction_test.go` already uses — copy them from that file rather
than inventing new ones, so the pgxmock expectations line up with the handler's
real SELECT list.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend; go test ./handlers/ -run TestQuery -v`
Expected: FAIL — `q=amt>50` is ignored, so `WithArgs` sees two args where the
test expects three, and `queryDiagnostics` is absent.

- [ ] **Step 3: Add the sink adapter**

Create `backend/handlers/query_sink.go`:

```go
package handlers

import "github.com/fintrak/backend/internal/query"

// txnFilterSink adapts txnFilter to query.Sink. It exists so the query package
// never imports handlers, and so txnFilter keeps its existing unexported method
// names — the methods below are pinned by many pgxmock tests that assert exact
// SQL strings and argument order.
type txnFilterSink struct{ f *txnFilter }

func (s txnFilterSink) Param(clause string, value any) { s.f.param(clause, value) }
func (s txnFilterSink) Clause(format string, value any) string {
	return s.f.clause(format, value)
}
func (s txnFilterSink) AnyOf(clauses []string) { s.f.anyOf(clauses) }
func (s txnFilterSink) Params(clause string, values ...any) {
	s.f.params(clause, values...)
}
func (s txnFilterSink) Raw(clause string) { s.f.raw(clause) }

// compileQuery parses and compiles the q= parameter onto f, returning any
// diagnostics. It is called last in txnQueryFilter so the query's placeholders
// follow the existing ones and the argument order the existing tests pin is
// unchanged for a request with no q.
func compileQuery(q string, f *txnFilter) []query.Diagnostic {
	if q == "" {
		return nil
	}
	expr, diags := query.Parse(q)
	diags = append(diags, query.Compile(expr, txnFilterSink{f})...)
	return diags
}
```

- [ ] **Step 4: Wire it into `txnQueryFilter`**

In `backend/handlers/transaction.go`, change the signature and add the call.
`txnQueryFilter` currently returns `(*txnFilter, *uuid.UUID, bool)`; it must also
return the diagnostics:

```go
func txnQueryFilter(c *gin.Context, userID uuid.UUID) (*txnFilter, *uuid.UUID, []query.Diagnostic, bool) {
```

Add near the end of the function body, **after** every existing clause has been
appended and before the closing `return`:

```go
	// q= is compiled last so its placeholders follow the parameter clauses
	// above. It is never a 4xx: an unusable term is dropped and returned as a
	// diagnostic, because a dropped constraint would otherwise widen the result
	// set with nothing to tell the user.
	diags := compileQuery(c.Query("q"), f)
```

and change the function's final return to:

```go
	return f, recurringID, diags, true
```

Update the early `return nil, nil, false` statements (there are several, one per
400 path) to `return nil, nil, nil, false`.

- [ ] **Step 5: Thread the diagnostics to the response**

In `GetTransactions`, the call site becomes:

```go
	f, recurringID, qDiags, ok := txnQueryFilter(c, auth.GetUserID(c))
	if !ok {
		return
	}
```

and the response becomes:

```go
	body := gin.H{
		"data":  transactions,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": pages,
	}
	// Omitted rather than sent as an empty array, so a caller can tell
	// "nothing was ignored" without a null check.
	if len(qDiags) > 0 {
		body["queryDiagnostics"] = qDiags
	}
	c.JSON(http.StatusOK, body)
```

- [ ] **Step 6: Update `transaction_export.go`**

Its call site is the same function, so update the destructuring and drop the
diagnostics (the export is a CSV; there is nowhere to render a banner):

```go
	f, _, _, ok := txnQueryFilter(c, auth.GetUserID(c))
```

Add one line to the export's godoc noting that `q` is honoured and that ignored
terms are not reported in a CSV.

- [ ] **Step 7: Run the handler tests**

Run: `cd backend; go test ./handlers/`
Expected: PASS, **including every pre-existing transaction test**. If an
existing test fails on argument order, `compileQuery` is being called before
some existing clause is appended; move it to the end.

- [ ] **Step 8: Run the whole backend suite and vet**

Run: `cd backend; go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add backend/handlers backend/internal/query
git commit -m "feat(transactions): honour the q= query parameter"
```

---

### Task 4: Document `q` in the spec and add it to the shared client

**Files:**
- Modify: `backend/openapi.yaml` (the `q` parameter on `GET /transactions` at ~line 1068 and on `GET /transactions/export` at ~line 1610)
- Modify: `client/api/endpoints_transactions.go`
- Test: `client/api/endpoints_transactions_test.go`

**Interfaces:**
- Consumes: the `q` parameter as shipped in Task 3.
- Produces: `api.TransactionFilter.Query string` and `func (f *TransactionFilter) SetQuery(q string) *TransactionFilter`.

- [ ] **Step 1: Write the failing client test**

Append to `client/api/endpoints_transactions_test.go`, following that file's
existing stub style:

```go
// TestTransactionQueryParamIsSent pins that the query language reaches the API
// as a single q= parameter, URL-encoded — the `>` in `amt>50` must not be
// allowed to truncate the string.
func TestTransactionQueryParamIsSent(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		writeJSON(w, `{"data":[],"total":0,"page":1,"pages":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.ListTransactions(context.Background(), TransactionFilter{Query: `cat:8a3f amt>50`}); err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if gotPath != "/transactions" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "q=") {
		t.Fatalf("raw query = %q, want a q= parameter", gotQuery)
	}
	// The raw form is encoded; the decoded value is the expression verbatim.
	decoded, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if got := decoded.Get("q"); got != `cat:8a3f amt>50` {
		t.Errorf("q = %q, want the expression verbatim", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd client; go test ./api/ -run TestTransactionQueryParam -v`
Expected: FAIL — `TransactionFilter` has no `Query` field.

- [ ] **Step 3: Add the field**

In `client/api/endpoints_transactions.go`, add to `TransactionFilter` after the
`Search` field:

```go
	// Query is the transaction query language, evaluated server-side: a
	// space-separated, AND-ed list of `field:value` terms (cat, group, acct,
	// payee, tag, type, amt, date, linked, recurring, desc, note) plus bare
	// words, which are free-text search. Values are **ids, not names** — the
	// server resolves no names, so a caller must resolve "Groceries" to a
	// category id first. Unusable terms are dropped, never rejected, and
	// reported in the response's queryDiagnostics.
	Query string
```

and add `.setQuery("q", f.Query)` to the `apply` chain, after
`setQuery("search", f.Search)`.

Add the setter, matching the file's existing setter style:

```go
// SetQuery sets the q= query language and returns the filter for chaining.
func (f *TransactionFilter) SetQuery(q string) *TransactionFilter {
	f.Query = q
	return f
}
```

- [ ] **Step 4: Run the client tests and the spec-parity guard**

Run: `cd client; go test ./...`
Expected: PASS. `TestRouteTableMatchesTheSpec` compares routes, not parameters,
so a new query parameter does not need a new `routeCase` — but the export
route's case must still hit its documented path.

- [ ] **Step 5: Document the parameter in openapi.yaml**

In the `parameters:` list of `GET /transactions`, add:

```yaml
        - in: query
          name: q
          schema:
            type: string
            maxLength: 2000
          description: >
            A transaction query expression, evaluated server-side: a
            space-separated, AND-ed list of terms. A term is
            `field:value` (fields: desc, note, cat, group, acct, payee, tag,
            type, amt, date, linked, recurring), optionally prefixed with
            `not`, or `field<op>value` where op is one of = != > >= < <= ~.
            A value may be a comma-separated list, which matches any of them.
            A bare word is a free-text substring search over description,
            notes, payee name and tags. Values are ids, not names: `cat` and
            `payee` take a uuid (or the sentinels "uncategorized" / "none"),
            and `tag` takes a tag name because tags have no ids. `amt` is in
            major units ("50", "50.75"); `date` is YYYY-MM-DD. An unusable
            term is never an error: it is dropped and reported in
            `queryDiagnostics`, so an over-broad term can widen the result set
            and the caller is told what was ignored. Max 32 terms.
```

Add the same block to `GET /transactions/export`'s `parameters:` list (that
operation already duplicates the whole list), minus nothing — the parameter means
the same thing on both.

- [ ] **Step 6: Run the parity and docs gates**

Run: `make openapi-check docs-check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add client/api backend/openapi.yaml
git commit -m "feat(client): expose the q= query language; document it in the spec"
```

---

### Task 5: Expose `q` to the MCP server

**Files:**
- Modify: `mcp/internal/mcpserver/tools_transactions.go`
- Test: `mcp/internal/mcpserver/tools_test.go`

**Interfaces:**
- Consumes: `api.TransactionFilter.Query` from Task 4.
- Produces: a `q` argument on the `list_transactions` tool.

- [ ] **Step 1: Run the audit to see it fail**

Run: `cd mcp; go test ./internal/mcpserver/ -run TestTool -v`
Expected: PASS at this point — `tools_test.go` audits that every *route* has a
tool and that each tool's declared arguments match what the stub receives, so it
will not fail until the argument is declared but not forwarded, or vice versa.
Run it to establish the baseline before editing.

- [ ] **Step 2: Declare the argument**

In `listTransactionsArgs`, add after `Search`:

```go
	// Query is the query language, and the one argument whose values are ids
	// rather than names: the server resolves no names, so the model must pass
	// the id it got from list_categories / list_payees / list_accounts.
	Query string `json:"q,omitempty" jsonschema:"a query expression: space-separated AND-ed terms, each 'field:value' or 'field<op>value' (op is one of = != > >= < <= ~); fields are cat, group, acct, payee, tag, type, amt, date, linked, recurring, desc, note; a comma-separated value matches any of them; a bare word is a free-text substring search. Values are IDS, not names — use the ids from list_categories, list_payees, list_accounts. tag takes a tag name. amt is in major units, e.g. \"50.75\". Unusable terms are dropped, not rejected; use the q form only when the structured arguments above cannot express the filter"`
```

- [ ] **Step 3: Forward it**

In `installListTransactions`, add `Query: in.Query,` to the
`api.TransactionFilter{...}` literal.

- [ ] **Step 4: Extend the tool description**

The `list_transactions` `Description` currently enumerates the filterable
dimensions. Append one sentence:

```go
				" It also accepts a typed query expression (q) for filters the named " +
				"arguments cannot express — amount ranges, several categories at once, " +
				"or negated terms; that expression takes ids rather than names.",
```

- [ ] **Step 5: Add the argument to the audit's expected set**

`tools_test.go` asserts the exact argument set each tool sends. Add a case to
`argumentCases` for `list_transactions` that passes `q` and expects it on the
wire, following the file's existing case style:

```go
	{
		tool: "list_transactions",
		args: map[string]any{"q": "cat:8a3f amt>50"},
		want: map[string]string{"q": "cat:8a3f amt>50"},
	},
```

- [ ] **Step 6: Run the MCP suite**

Run: `cd mcp; go test ./... && go vet ./...`
Expected: PASS. `mcp/internal/readonly` is unaffected: the route is still a
`GET /transactions` and the `SideEffect` string still describes it correctly.

- [ ] **Step 7: Commit**

```bash
git add mcp
git commit -m "feat(mcp): expose the q= query language to list_transactions"
```

---

### Task 6: The grammar in TypeScript, pinned by the same corpus

**Files:**
- Create: `frontend/src/lib/query/parse.ts`
- Create: `frontend/src/lib/query/fields.ts`
- Test: `frontend/src/lib/query/parse.test.ts`

**Interfaces:**
- Consumes: `backend/internal/query/testdata/corpus.json` (the same file Task 1 wrote).
- Produces:
  ```ts
  export const FIELD_PLAIN = "plain";
  export interface QueryTerm { field: string; op: QueryOp; values: string[]; negated: boolean; position: number; raw: string; }
  export interface QueryDiagnostic { term: string; code: string; message: string; position: number; }
  export interface ParsedQuery { terms: QueryTerm[]; diagnostics: QueryDiagnostic[]; }
  export function parseQuery(q: string): ParsedQuery;
  ```

- [ ] **Step 1: Write the failing corpus test**

Create `frontend/src/lib/query/parse.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { parseQuery } from "./parse";

// The same file the Go parser tests read. A field or operator added on one side
// without the other fails both suites, which is the whole point of keeping the
// grammar in one JSON file rather than in two implementations' heads.
const CORPUS = JSON.parse(
  readFileSync(
    fileURLToPath(new URL("../../../backend/internal/query/testdata/corpus.json", import.meta.url)),
    "utf-8",
  ),
) as { cases: CorpusCase[] };

interface CorpusCase {
  name: string;
  surface: string;
  canonical: string;
  terms: Array<{ field: string; op: string; values: string[]; negated: boolean; position: number }>;
  diagnostics: Array<{ term: string; code: string; message: string; position: number }>;
}

describe("parseQuery", () => {
  it("has a non-empty corpus to check against", () => {
    expect(CORPUS.cases.length).toBeGreaterThan(0);
  });

  for (const tc of CORPUS.cases) {
    it(`matches the corpus: ${tc.name}`, () => {
      const got = parseQuery(tc.surface);
      expect(got.terms.map((t) => ({ field: t.field, op: t.op, values: t.values, negated: t.negated, position: t.position })))
        .toEqual(tc.terms);
      expect(got.diagnostics.map((d) => ({ term: d.term, code: d.code, position: d.position })))
        .toEqual(tc.diagnostics.map((d) => ({ term: d.term, code: d.code, position: d.position })));
    });
  }
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend; bun run test src/lib/query/parse.test.ts`
Expected: FAIL — `Cannot find module './parse'`.

- [ ] **Step 3: Write the field table**

Create `frontend/src/lib/query/fields.ts`:

```ts
  // fields.ts owns QueryOp, not parse.ts: parse.ts imports FIELD_TABLE (a
  // value) from fields.ts, so declaring the type in parse.ts and importing
  // it back would make the two modules circular at runtime over a type
  // that is erased anyway.
  export type QueryOp = "=" | "!=" | ">" | ">=" | "<" | "<=" | "~";

  export type FieldKind = "text" | "uuid" | "enum" | "amount" | "date" | "tags";

export interface FieldDef {
  /** False for the `plain` pseudo-field, which the parser synthesises. */
  userTyped: boolean;
  kind: FieldKind;
  ops: QueryOp[];
  enum?: string[];
}

const EQ: QueryOp[] = ["=", "!="];
const ORDER: QueryOp[] = ["=", "!=", ">", ">=", "<", "<="];
const TEXT: QueryOp[] = ["=", "!=", "~"];

export const FIELD_TABLE: Record<string, FieldDef> = {
  plain: { userTyped: false, kind: "text", ops: TEXT },
  desc: { userTyped: true, kind: "text", ops: TEXT },
  note: { userTyped: true, kind: "text", ops: TEXT },
  cat: { userTyped: true, kind: "uuid", ops: EQ },
  group: { userTyped: true, kind: "uuid", ops: EQ },
  acct: { userTyped: true, kind: "uuid", ops: EQ },
  payee: { userTyped: true, kind: "uuid", ops: EQ },
  tag: { userTyped: true, kind: "tags", ops: EQ },
  type: { userTyped: true, kind: "enum", ops: EQ, enum: ["debit", "credit"] },
  linked: { userTyped: true, kind: "enum", ops: EQ, enum: ["true", "false"] },
  recurring: { userTyped: true, kind: "enum", ops: EQ, enum: ["linked", "unlinked"] },
  amt: { userTyped: true, kind: "amount", ops: ORDER },
  date: { userTyped: true, kind: "date", ops: ORDER },
};

/** The fields the autocomplete offers, in the order the grammar sheet lists them. */
export const USER_FIELDS = Object.entries(FIELD_TABLE)
  .filter(([, def]) => def.userTyped)
  .map(([name]) => name);

export function userField(name: string): FieldDef | undefined {
  const def = FIELD_TABLE[name];
  return def && def.userTyped ? def : undefined;
}

export function allowsOp(def: FieldDef, op: QueryOp): boolean {
  return def.ops.includes(op);
}
```

- [ ] **Step 4: Write the parser**

Create `frontend/src/lib/query/parse.ts`:

```ts
import { allowsOp, userField, type FieldDef, type QueryOp } from "./fields";

// QueryOp is re-exported so callers can take the whole query surface from this
// module; the type itself is declared in fields.ts to keep the two acyclic.
export type { QueryOp };

export const MAX_QUERY_CHARS = 2000;
export const MAX_TERMS = 32;

export const FIELD_PLAIN = "plain";

export interface QueryTerm {
  field: string;
  op: QueryOp;
  values: string[];
  negated: boolean;
  position: number;
  raw: string;
}

export interface QueryDiagnostic {
  term: string;
  code: string;
  message: string;
  position: number;
}

export interface ParsedQuery {
  terms: QueryTerm[];
  diagnostics: QueryDiagnostic[];
}

export const DIAG = {
  unknownField: "unknown_field",
  badOperator: "bad_operator",
  missingValue: "missing_value",
  malformedAmount: "malformed_amount",
  malformedDate: "malformed_date",
  ambiguousValue: "ambiguous_value",
  tooLong: "too_long",
} as const;

// Longest first, so ">=" wins over ">".
const OPS: Array<[string, QueryOp]> = [
  [">=", ">="],
  ["<=", "<="],
  ["!=", "!="],
  ["~", "~"],
  [">", ">"],
  ["<", "<"],
  ["=", "="],
];

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
// One optional sign, digits on both sides of the point, at most two fraction
// digits — the same grammar money.Parse accepts, mirrored so a client cannot
// submit what the API would refuse.
const AMOUNT_RE = /^[+-]?\d+(\.\d{1,2})?$/;

export function isUuid(v: string): boolean {
  return UUID_RE.test(v);
}

/**
 * parseQuery tokenizes a query and returns the terms it understood plus a
 * diagnostic for every term it dropped. It never throws: a term it cannot
 * understand is dropped and reported, because a dropped constraint silently
 * widens a result set and the user would read the whole ledger as the answer.
 */
export function parseQuery(input: string): ParsedQuery {
  const terms: QueryTerm[] = [];
  const diagnostics: QueryDiagnostic[] = [];
  let q = input;

  if (q.length > MAX_QUERY_CHARS) {
    diagnostics.push({
      term: q.slice(0, MAX_QUERY_CHARS),
      code: DIAG.tooLong,
      message: "query is longer than 2000 characters; the rest was ignored",
      position: 0,
    });
    q = q.slice(0, MAX_QUERY_CHARS);
  }

  let i = 0;
  const n = q.length;
  const isSpace = (c: string) => c === " " || c === "\t";
  const skipSpaces = () => {
    while (i < n && isSpace(q[i])) i++;
  };

  while (true) {
    skipSpaces();
    if (i >= n) break;

    if (terms.length >= MAX_TERMS) {
      diagnostics.push({
        term: q.slice(i).trim(),
        code: DIAG.tooLong,
        message: "query has more than 32 terms; the rest was ignored",
        position: i,
      });
      break;
    }

    const start = i;
    let negated = false;
    const firstWord = readWord(q, i);
    if (firstWord.word.toLowerCase() === "not") {
      negated = true;
      i = firstWord.next;
      skipSpaces();
    }

    const word = readWord(q, i);
    const def = userField(word.word.toLowerCase());

    // `field:value`
    if (word.word !== "" && word.next < n && q[word.next] === ":") {
      const field = word.word.toLowerCase();
      const colonAt = word.next;
      i = colonAt + 1;
      skipSpaces();
      if (i >= n) {
        diagnostics.push({ term: q.slice(start, colonAt + 1), code: DIAG.missingValue, message: `${field}: needs a value`, position: start });
        continue;
      }
      const read = readValue(q, i);
      i = read.end;
      if (!def) {
        diagnostics.push({ term: q.slice(start, read.end), code: DIAG.unknownField, message: `unknown field ${field}`, position: start });
        continue;
      }
      const err = validate(def, field, read.values, "=");
      if (err) {
        diagnostics.push({ term: q.slice(start, read.end), ...err, position: start });
        continue;
      }
      terms.push({ field, op: "=", values: read.values, negated, position: start, raw: q.slice(start, read.end) });
      continue;
    }

    // `field<op>value`. The operator sits just past the field name, which is
    // where readWord stopped. Matching at i would never fire, because i is
    // the start of the name, not the operator.
    const op = matchOp(q, word.next);
    if (op) {
      const field = word.word.toLowerCase();
      const afterOp = word.next + op.symbol.length;
      let j = afterOp;
      while (j < n && isSpace(q[j])) j++;
      if (j >= n) {
        diagnostics.push({ term: q.slice(start, afterOp), code: DIAG.missingValue, message: `${field} needs a value`, position: start });
        i = afterOp;
        continue;
      }
      const read = readValue(q, j);
      const end = read.end;
      if (!def) {
        diagnostics.push({ term: q.slice(start, end), code: DIAG.unknownField, message: `unknown field ${field}`, position: start });
        i = end;
        continue;
      }
      if (!allowsOp(def, op.op)) {
        diagnostics.push({ term: q.slice(start, end), code: DIAG.badOperator, message: `${field} does not support ${op.symbol}`, position: start });
        i = end;
        continue;
      }
      const err = validate(def, field, read.values, op.op);
      if (err) {
        diagnostics.push({ term: q.slice(start, end), ...err, position: start });
        i = end;
        continue;
      }
      terms.push({ field, op: op.op, values: read.values, negated, position: start, raw: q.slice(start, read.end) });
      i = end;
      continue;
      continue;
    }

    // Bare words: plain search.
    const words: string[] = [];
    let j = i;
    while (j < n) {
      const w = readWord(q, j);
      if (w.word === "") break;
      words.push(w.word);
      j = w.next;
    }
    if (words.length === 0) {
      i++; // a byte we cannot start a token with
      continue;
    }
    terms.push({ field: FIELD_PLAIN, op: "~", values: words, negated, position: start, raw: words.join(" ") });
    i = j;
  }

  return { terms, diagnostics };
}

function readWord(q: string, i: number): { word: string; next: number } {
  let j = i;
  while (j < q.length) {
    const c = q[j];
    if (c === " " || c === "\t" || c === '"' || c === ",") break;
    if (c === ">" || c === "<" || c === "=" || c === "!" || c === "~") break;
    j++;
  }
  return { word: q.slice(i, j), next: j };
}

function matchOp(q: string, i: number): { symbol: string; op: QueryOp } | null {
  const rest = q.slice(i);
  for (const [symbol, op] of OPS) {
    if (rest.startsWith(symbol)) return { symbol, op };
  }
  return null;
}

function readValue(q: string, i: number): { end: number; values: string[] } {
  if (q[i] === '"') {
    let out = "";
    let j = i + 1;
    while (j < q.length) {
      if (q[j] === "\\" && j + 1 < q.length) {
        out += q[j + 1];
        j += 2;
        continue;
      }
      if (q[j] === '"') {
        j++;
        break;
      }
      out += q[j];
      j++;
    }
    return { end: j, values: [out] };
  }
  let j = i;
  while (j < q.length && q[j] !== " " && q[j] !== "\t" && q[j] !== '"') j++;
  const values = q
    .slice(i, j)
    .split(",")
    .map((p) => p.trim())
    .filter((p) => p !== "");
  if (values.length === 0) return { end: j, values: [q.slice(i, j)] };
  return { end: j, values };
}

/** validate returns a diagnostic body (code + message) or null. */
function validate(def: FieldDef, field: string, values: string[], _op: QueryOp): { code: string; message: string } | null {
  if (values.length === 0) return { code: DIAG.missingValue, message: `${field}: needs a value` };
  for (const v of values) {
    switch (def.kind) {
      case "text":
        break;
      case "uuid":
        if (v === "none" || v === "uncategorized") break;
        if (!isUuid(v)) {
          return { code: DIAG.unknownField, message: `${field}: ${JSON.stringify(v)} is not an id; resolve the name first (ids are uuid, "none" or "uncategorized")` };
        }
        break;
      case "enum":
        if (!def.enum?.includes(v)) return { code: "unresolved_value", message: `${field}: ${JSON.stringify(v)} is not one of ${def.enum}` };
        break;
      case "amount":
        if (!AMOUNT_RE.test(v)) return { code: DIAG.malformedAmount, message: `${field}: ${JSON.stringify(v)} is not an amount (e.g. 50 or 50.75)` };
        break;
      case "date":
        if (!DATE_RE.test(v) || !isInDateWindow(v)) {
          return { code: DIAG.malformedDate, message: `${field}: ${JSON.stringify(v)} is not a usable date (must be between 1900-01-01 and one year from today)` };
        }
        break;
      case "tags":
        break;
    }
  }
  return null;
}

// The same window validation.CheckTransactionDate applies server-side. The
// bounds are duplicated rather than fetched: a shared helper would put a date
// range in the API contract to check one string, and todayLocalISO() is the
// frontend's own "today" (see src/lib/dates.ts — never toISOString, which is
// yesterday east of UTC before 05:30).
function isInDateWindow(value: string): boolean {
  const min = Date.UTC(1900, 0, 1);
  const [y, m, d] = value.split("-").map(Number);
  const t = Date.UTC(y, m - 1, d);
  if (Number.isNaN(t)) return false;
  if (t < min) return false;
  const today = new Date();
  const max = Date.UTC(today.getFullYear() + 1, today.getMonth(), today.getDate());
  return t <= max;
}
```

- [ ] **Step 5: Run the corpus test**

Run: `cd frontend; bun run test src/lib/query/parse.test.ts`
Expected: PASS. The Go corpus stores positions as **byte** offsets and the
TypeScript parser as UTF-16 code-unit offsets; they agree for the ASCII corpus.
If a future corpus case uses a non-ASCII term, the `position` assertions will
diverge — that is a known limitation, and the corpus must stay ASCII.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/query
git commit -m "feat(query): the query grammar in TypeScript, pinned by the shared corpus"
```

---

### Task 7: Resolve names to ids, and serialize

**Files:**
- Create: `frontend/src/lib/query/resolve.ts`
- Create: `frontend/src/lib/query/serialize.ts`
- Test: `frontend/src/lib/query/resolve.test.ts`

**Interfaces:**
- Consumes: `parseQuery`, `QueryTerm`, `QueryDiagnostic` from Task 6; `DomainDataContext`; `TagCount[]`; `PERIOD_VALUES` from `src/lib/dates.ts`.
- Produces:
  ```ts
  export interface ResolveSource {
    accounts: Account[]; categories: Category[]; groups: CategoryGroup[]; payees: Payee[];
    tags: string[];          // tag names, from the page's TagCount[]
  }
  export function resolveQuery(parsed: ParsedQuery, src: ResolveSource): { terms: QueryTerm[]; diagnostics: QueryDiagnostic[]; };
  export function serializeQuery(terms: QueryTerm[]): string;
  ```

- [ ] **Step 1: Write the failing resolve test**

Create `frontend/src/lib/query/resolve.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { parseQuery } from "./parse";
import { resolveQuery, serializeQuery } from "./resolve";

const src = {
  accounts: [{ id: "11111111-1111-1111-1111-111111111111", name: "Checking" }] as never,
  categories: [
    { id: "22222222-2222-2222-2222-222222222222", name: "Groceries", groupId: "33333333-3333-3333-3333-333333333333" },
    { id: "44444444-4444-4444-4444-444444444444", name: "Groceries", groupId: "55555555-5555-5555-5555-555555555555" },
  ] as never,
  groups: [
    { id: "33333333-3333-3333-3333-333333333333", name: "Food" },
    { id: "55555555-5555-5555-5555-555555555555", name: "Home" },
  ] as never,
  payees: [{ id: "66666666-6666-6666-6666-666666666666", name: "Whole Foods" }] as never,
  tags: ["vacation"],
};

function run(q: string) {
  return resolveQuery(parseQuery(q), src);
}

describe("resolveQuery", () => {
  it("resolves a category name to its id", () => {
    const { terms, diagnostics } = run("cat:Food/Groceries");
    expect(diagnostics).toEqual([]);
    expect(terms[0].values).toEqual(["22222222-2222-2222-2222-222222222222"]);
  });

  it("resolves an account and a quoted payee", () => {
    const { terms, diagnostics } = run('acct:Checking payee:"Whole Foods"');
    expect(diagnostics).toEqual([]);
    expect(serializeQuery(terms)).toBe(
      "acct:11111111-1111-1111-1111-111111111111 payee:66666666-6666-6666-6666-666666666666",
    );
  });

  it("keeps the filter but reports an ambiguous bare name", () => {
    // Review Focus #4: an ambiguous name must still filter (as an OR) and say
    // so — never silently pick one, never silently widen to all payees.
    const { terms, diagnostics } = run("cat:Groceries");
    expect(terms[0].values).toHaveLength(2);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("ambiguous_value");
  });

  it("drops a name that matches nothing and reports it", () => {
    const { terms, diagnostics } = run("payee:Costco amt>50");
    expect(terms.map((t) => t.field)).toEqual(["amt"]);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("unresolved_value");
  });

  it("leaves a bare word and a tag alone", () => {
    const { terms, diagnostics } = run("coffee tag:vacation");
    expect(diagnostics).toEqual([]);
    expect(terms[0].field).toBe("plain");
    expect(terms[1].values).toEqual(["vacation"]);
  });

  it("converts an amount to minor units with integer arithmetic", () => {
    const { terms } = run("amt>50.75");
    expect(terms[0].values).toEqual(["5075"]);
    expect(serializeQuery(terms)).toBe("amt>=5075");
  });

  it("converts a named period into concrete date bounds", () => {
    const { terms, diagnostics } = run("date:this-month");
    expect(diagnostics).toEqual([]);
    expect(terms).toHaveLength(2);
    expect(terms[0].op).toBe(">=");
    expect(terms[1].op).toBe("<=");
    expect(serializeQuery(terms)).toMatch(/^date>=\d{4}-\d{2}-\d{2} date<=\d{4}-\d{2}-\d{2}$/);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend; bun run test src/lib/query/resolve.test.ts`
Expected: FAIL — `Cannot find module './resolve'`.

- [ ] **Step 3: Write the resolver**

Create `frontend/src/lib/query/resolve.ts`:

```ts
import { FIELD_PLAIN, type ParsedQuery, type QueryDiagnostic, type QueryTerm } from "./parse";
import { FIELD_TABLE } from "./fields";
import { periodRange, PERIOD_VALUES, todayLocalISO } from "@/lib/dates";
import type { Account, Category, CategoryGroup, Payee } from "@/types";

export interface ResolveSource {
  accounts: Account[];
  categories: Category[];
  groups: CategoryGroup[];
  payees: Payee[];
  /** Tag names, from the page's TagCount[] (there is no tag table). */
  tags: string[];
}

/** resolveQuery rewrites names to ids and periods to concrete dates. */
export function resolveQuery(parsed: ParsedQuery, src: ResolveSource): { terms: QueryTerm[]; diagnostics: QueryDiagnostic[] } {
  const terms: QueryTerm[] = [];
  const diagnostics = [...parsed.diagnostics];

  for (const term of parsed.terms) {
    if (term.field === FIELD_PLAIN) {
      terms.push(term);
      continue;
    }
    const def = FIELD_TABLE[term.field];
    if (!def) {
      diagnostics.push({ term: term.raw, code: "unknown_field", message: `unknown field ${term.field}`, position: term.position });
      continue;
    }
    if (def.kind === "tags") {
      terms.push(term);
      continue;
    }
    if (def.kind === "amount") {
      terms.push({ ...term, values: term.values.map(toMinorUnits) });
      continue;
    }
    if (def.kind === "date") {
      const expanded = expandDates(term, diagnostics);
      terms.push(...expanded);
      continue;
    }
    if (def.kind === "enum") {
      terms.push(term);
      continue;
    }
    // kind === "uuid": a name has to become an id, because the server resolves
    // no names (a curl or MCP caller sending a name gets no filter at all).
    const resolved: string[] = [];
    const ambiguous: string[] = [];
    for (const v of term.values) {
      if (v === "none" || v === "uncategorized" || isUuidish(v)) {
        resolved.push(v);
        continue;
      }
      const hits = lookup(term.field, v, src);
      if (hits.length === 0) {
        diagnostics.push({
          term: term.raw,
          code: "unresolved_value",
          message: `no ${labelFor(term.field)} named ${JSON.stringify(v)}`,
          position: term.position,
        });
        continue;
      }
      if (hits.length > 1) {
        ambiguous.push(v);
      }
      resolved.push(...hits);
    }
    if (ambiguous.length > 0) {
      diagnostics.push({
        term: term.raw,
        code: "ambiguous_value",
        message: `${ambiguous.map((v) => JSON.stringify(v)).join(", ")} matched more than one ${labelFor(term.field)}; keeping all of them. Use ${term.field}:Group/Name to pick one.`,
        position: term.position,
      });
    }
    if (resolved.length > 0) {
      terms.push({ ...term, values: resolved });
    }
  }

  return { terms, diagnostics };
}

function labelFor(field: string): string {
  switch (field) {
    case "cat":
      return "category";
    case "group":
      return "category group";
    case "acct":
      return "account";
    case "payee":
      return "payee";
    default:
      return field;
  }
}

function isUuidish(v: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v);
}

/** lookup resolves one name, accepting the `Group/Name` qualified spelling. */
function lookup(field: string, raw: string, src: ResolveSource): string[] {
  const slash = raw.indexOf("/");
  const groupName = slash > 0 ? raw.slice(0, slash).toLowerCase() : null;
  const name = (slash > 0 ? raw.slice(slash + 1) : raw).toLowerCase();

  switch (field) {
    case "cat": {
      const groupId = groupName ? src.groups.find((g) => g.name.toLowerCase() === groupName)?.id : null;
      return src.categories.filter((c) => c.name.toLowerCase() === name && (groupId ? c.groupId === groupId : true)).map((c) => c.id);
    }
    case "group": {
      const byId = src.groups.find((g) => g.id.toLowerCase() === raw.toLowerCase());
      if (byId) return [byId.id];
      return src.groups.filter((g) => g.name.toLowerCase() === name).map((g) => g.id);
    }
    case "acct":
      return src.accounts.filter((a) => a.name.toLowerCase() === name).map((a) => a.id);
    case "payee":
      return src.payees.filter((p) => p.name.toLowerCase() === name).map((p) => p.id);
    default:
      return [];
  }
}

/**
 * toMinorUnits converts decimal major units to integer minor units without a
 * float: a query for 50.75 must bind 5075 and never round through a double.
 */
export function toMinorUnits(v: string): string {
  const negative = v.startsWith("-");
  const body = v.replace(/^[+-]/, "");
  const [whole = "0", frac = ""] = body.split(".");
  const minor = Number(whole) * 100 + Number(frac.padEnd(2, "0") || "0");
  return `${negative ? "-" : ""}${minor}`;
}

function expandDates(term: QueryTerm, diagnostics: QueryDiagnostic[]): QueryTerm[] {
  const isPeriod = (v: string) => (PERIOD_VALUES as readonly string[]).includes(v);
  if (!term.values.every(isPeriod)) {
    return [term];
  }
  const range = periodRange(term.values[0]);
  if (!range) {
    diagnostics.push({ term: term.raw, code: "malformed_date", message: `${JSON.stringify(term.values[0])} is not a known period`, position: term.position });
    return [];
  }
  // A named period becomes concrete bounds, so a shared URL means the same
  // range it meant when it was written — the same choice Money Flow and the
  // calendar make when they store resolved dates in the URL.
  return [
    { ...term, op: ">=", values: [range.from], negated: false },
    { ...term, op: "<=", values: [range.to], negated: false },
  ];
}

/** serializeQuery renders terms as the q= value. */
export function serializeQuery(terms: QueryTerm[]): string {
  return terms
    .map((t) => {
      const value = t.values.map(quoteIfNeeded).join(",");
      const body = t.op === "=" ? `${t.field}:${value}` : `${t.field}${t.op}${value}`;
      return t.negated ? `not ${body}` : body;
    })
    .join(" ");
}

function quoteIfNeeded(v: string): string {
  return /[\s,"]/.test(v) ? `"${v.replace(/(["\\])/g, "\\$1")}"` : v;
}

/** todayLocalISO is re-exported so the hook does not import two date modules. */
export { todayLocalISO };
```

Check `periodRange`'s actual signature in `src/lib/dates.ts` — this plan assumes
`periodRange(period: string): { from: string; to: string } | null` and
`PERIOD_VALUES: string[]`. If it returns a different shape, adapt this function
and the test in Step 1 to the real one rather than changing `dates.ts`.

- [ ] **Step 4: Write the serializer test for quoting**

Append to `resolve.test.ts`:

```ts
describe("serializeQuery", () => {
  it("quotes a value containing a space and escapes the quote character", () => {
    expect(serializeQuery([{ field: "payee", op: "=", values: ['He said "hi"'], negated: false, position: 0, raw: "" }])).toBe(
      'payee:"He said \\"hi\\""',
    );
  });
  it("leaves a uuid unquoted", () => {
    expect(serializeQuery([{ field: "cat", op: "=", values: ["8a3f-1234"], negated: false, position: 0, raw: "" }])).toBe("cat:8a3f-1234");
  });
});
```

- [ ] **Step 5: Run the resolve tests**

Run: `cd frontend; bun run test src/lib/query/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/query
git commit -m "feat(query): resolve names to ids and serialize the canonical form"
```

---

### Task 8: The hook, and the offline-cache policy

**Files:**
- Create: `frontend/src/lib/query/useQueryLanguage.ts`
- Modify: `frontend/src/api/client.ts` (skip the cache write for a queried `/transactions` read)
- Test: `frontend/src/lib/query/useQueryLanguage.test.ts`

**Interfaces:**
- Consumes: `parseQuery`, `resolveQuery`, `serializeQuery` from Tasks 6–7; `api.getTransactions`; `TransactionsResponse`.
- Produces:
  ```ts
  export interface QueryLanguage {
    text: string;
    setText(next: string): void;
    /** The q= value to send, or "" when there is nothing to send. */
    q: string;
    /** Local (pre-request) plus server diagnostics, deduped by term+code. */
    diagnostics: QueryDiagnostic[];
    /** True when the caret sits in the final token, so it must not be reported. */
    inProgress: string | null;
    setInProgress(token: string | null): void;
    finalize(): void;
    clear(): void;
  }
  export function useQueryLanguage(opts: { source: ResolveSource; ownerQ: string }): QueryLanguage;
  ```
  `ownerQ` is the `q` the current response came from, so server diagnostics are
  only shown for the query that produced them — a late response for an old query
  must not annotate a new one.

- [ ] **Step 1: Write the failing in-progress test**

Create `frontend/src/lib/query/useQueryLanguage.test.ts`:

```ts
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useQueryLanguage } from "./useQueryLanguage";

const src = {
  accounts: [{ id: "11111111-1111-1111-1111-111111111111", name: "Checking" }],
  categories: [],
  groups: [],
  payees: [],
  tags: [],
} as never;

describe("useQueryLanguage in-progress tokens", () => {
  // Review Focus: typing must not strobe the warning banner.
  it("reports nothing while the caret is in the last token", () => {
    const { result } = renderHook(() => useQueryLanguage({ source: src, ownerQ: "" }));
    act(() => result.current.setText("catg"));
    act(() => result.current.setInProgress("catg"));
    expect(result.current.diagnostics).toEqual([]);
    expect(result.current.q).toBe("");
  });

  it("stays quiet on a half-typed field name", () => {
    const { result } = renderHook(() => useQueryLanguage({ source: src, ownerQ: "" }));
    act(() => result.current.setText("cat"));
    act(() => result.current.setInProgress("cat"));
    expect(result.current.diagnostics).toEqual([]);
  });

  it("reports once the token is finished", () => {
    const { result } = renderHook(() => useQueryLanguage({ source: src, ownerQ: "" }));
    act(() => result.current.setText("catg"));
    act(() => result.current.setInProgress(null)); // caret moved left
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["unknown_field"]);
  });

  it("finalize reports a half-typed value, so cat: + Enter is an error", () => {
    const { result } = renderHook(() => useQueryLanguage({ source: src, ownerQ: "" }));
    act(() => result.current.setText("cat:"));
    act(() => result.current.setInProgress("cat:"));
    expect(result.current.diagnostics).toEqual([]);
    act(() => result.current.finalize());
    expect(result.current.diagnostics.map((d) => d.code)).toEqual(["missing_value"]);
  });

  it("drops the in-progress token from the wire", () => {
    const { result } = renderHook(() => useQueryLanguage({ source: src, ownerQ: "" }));
    act(() => result.current.setText("amt>50 am"));
    act(() => result.current.setInProgress("am"));
    expect(result.current.q).toBe("amt>=5000");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend; bun run test src/lib/query/useQueryLanguage.test.ts`
Expected: FAIL — `Cannot find module './useQueryLanguage'`.

- [ ] **Step 3: Write the hook**

Create `frontend/src/lib/query/useQueryLanguage.ts`:

```ts
import { useCallback, useMemo, useState } from "react";
import { parseQuery, type QueryDiagnostic } from "./parse";
import { resolveQuery, serializeQuery, type ResolveSource } from "./resolve";

export interface QueryLanguage {
  text: string;
  setText(next: string): void;
  q: string;
  diagnostics: QueryDiagnostic[];
  inProgress: string | null;
  setInProgress(token: string | null): void;
  finalize(): void;
  clear(): void;
  setServerDiagnostics(list: QueryDiagnostic[]): void;
}

export function useQueryLanguage(opts: { source: ResolveSource; ownerQ: string }): QueryLanguage {
  const { source, ownerQ } = opts;
  const [text, setText] = useState("");
  const [inProgress, setInProgress] = useState<string | null>(null);
  const [finalized, setFinalized] = useState(false);
  const [server, setServer] = useState<{ q: string; list: QueryDiagnostic[] }>({ q: "", list: [] });

  const local = useMemo(() => {
    // The token under the caret is in progress, not invalid: it is neither
    // reported nor serialized, so the server never sees a half-typed term.
    // Pressing Enter finalizes it, which is when `cat:` becomes an error.
    const toParse = inProgress && !finalized ? text.slice(0, text.length - inProgress.length) : text;
    const parsed = parseQuery(toParse);
    const resolved = resolveQuery(parsed, source);
    return { q: serializeQuery(resolved.terms), diagnostics: resolved.diagnostics };
  }, [text, inProgress, finalized, source]);

  const diagnostics = useMemo(() => {
    const all = [...local.diagnostics];
    // Only the response's own query may annotate the box. A slow response for
    // a query the user has already replaced must not report against the new one.
    if (server.q === ownerQ && ownerQ !== "") {
      all.push(...server.list);
    }
    const seen = new Set<string>();
    return all.filter((d) => {
      const key = `${d.code}:${d.term}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }, [local.diagnostics, server, ownerQ]);

  return {
    text,
    setText,
    q: local.q,
    diagnostics,
    inProgress,
    setInProgress,
    finalize: useCallback(() => setFinalized(true), []),
    clear: useCallback(() => {
      setText("");
      setInProgress(null);
      setFinalized(false);
      setServer({ q: "", list: [] });
    }, []),
    setServerDiagnostics: useCallback((list: QueryDiagnostic[]) => setServer({ q: ownerQ, list }), [ownerQ]),
  };
}
```

- [ ] **Step 4: Skip the offline cache write for a queried read**

In `frontend/src/api/client.ts`, find the `writeOffline` call inside the shared
`request` path and gate it. The reason, in a comment: the cache key is the full
URL, so every distinct `q` would occupy one of the 40 slots in `offlineCache.ts`
and compete for the 2 MB cap — a user exploring queries would evict the default
unfiltered view they would otherwise want offline.

```ts
// A queried read is not cached: the cache key is the full URL, so every
// distinct q would take one of the 40 slots and could evict the default view.
const queriedLedgerRead = url.startsWith("/transactions") && new URL(url, "http://x").searchParams.has("q");
if (!queriedLedgerRead) writeOffline(owner, url, data);
```

Adjust the argument order to `writeOffline`'s real signature. If `writeOffline`
takes the parsed body rather than a string, gate the call site identically.

- [ ] **Step 5: Run the hook and cache tests**

Run: `cd frontend; bun run test src/lib/query/ src/api/`
Expected: PASS. If an existing `offlineCache` or `client` test asserts that every
cacheable path is written, update it to assert the new exception explicitly
rather than deleting the assertion.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/query frontend/src/api
git commit -m "feat(query): the language hook; keep queried reads out of the offline cache"
```

---

### Task 9: The input, the autocomplete, and the banner

**Files:**
- Create: `frontend/src/lib/query/QueryInput.tsx`
- Create: `frontend/src/lib/query/QueryDiagnostics.tsx`
- Modify: `frontend/src/components/Transactions/TransactionFilters.tsx`
- Modify: `frontend/src/components/Transactions/transactionConstants.ts`
- Test: `frontend/src/lib/query/QueryInput.test.tsx`
- Test: `frontend/src/components/Transactions/TransactionFilters.test.tsx`

**Interfaces:**
- Consumes: `useQueryLanguage` from Task 8; `cmdk` (`src/components/ui/command.tsx`); shadcn `Input`, `Button`, `Popover`.
- Produces: a `QueryInput` that owns the text, the autocomplete popup, the grammar sheet, and reports `onCommit(q: string)`.

- [ ] **Step 1: Write the failing autocomplete test**

Create `frontend/src/lib/query/QueryInput.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { QueryInput } from "./QueryInput";

const src = {
  accounts: [{ id: "11111111-1111-1111-1111-111111111111", name: "Checking" }],
  categories: [{ id: "22222222-2222-2222-2222-222222222222", name: "Groceries", groupId: "33333333-3333-3333-3333-333333333333" }],
  groups: [{ id: "33333333-3333-3333-3333-333333333333", name: "Food" }],
  payees: [{ id: "66666666-6666-6666-6666-666666666666", name: "Whole Foods" }],
  tags: ["vacation"],
} as never;

function setup() {
  const onCommit = vi.fn();
  render(<QueryInput source={src} onCommit={onCommit} diagnostics={[]} />);
  return { onCommit, input: screen.getByRole("textbox") };
}

describe("QueryInput", () => {
  it("suggests field names on a fresh token", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "ca");
    expect(await screen.findByText("cat")).toBeTruthy();
  });

  it("quotes an inserted value containing a space", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "payee:Whole");
    const option = await screen.findByText("Whole Foods");
    await user.click(option);
    expect((input as HTMLInputElement).value).toBe('payee:"Whole Foods"');
  });

  it("disambiguates an ambiguous category by group", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "cat:Grocer");
    const option = await screen.findByText("Food/Groceries");
    await user.click(option);
    expect((input as HTMLInputElement).value).toBe("cat:Food/Groceries");
  });

  // Enter must run the query, never accept a suggestion: the existing search
  // box ran on Enter and that reflex is worth more than autocomplete convenience.
  it("commits on Enter without accepting the highlighted suggestion", async () => {
    const user = userEvent.setup();
    const { input, onCommit } = setup();
    await user.type(input, "cat:Food/Groceries{Enter}");
    expect(onCommit).toHaveBeenCalledWith("cat:22222222-2222-2222-2222-222222222222");
  });

  it("accepts the highlighted suggestion on Tab", async () => {
    const user = userEvent.setup();
    const { input } = setup();
    await user.type(input, "cat:Food/Groceries ");
    await user.keyboard("{Tab}");
    // The suggestion was inserted, not submitted.
    expect(onCommit).not.toHaveBeenCalled();
  });

  it("opens the grammar sheet from the help button", async () => {
    const user = userEvent.setup();
    render(<QueryInput source={src} onCommit={vi.fn()} diagnostics={[]} />);
    await user.click(screen.getByRole("button", { name: /query syntax|help/i }));
    expect(await screen.findByText(/cat/)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend; bun run test src/lib/query/QueryInput.test.tsx`
Expected: FAIL — `Cannot find module './QueryInput'`.

- [ ] **Step 3: Write the banner**

Create `frontend/src/lib/query/QueryDiagnostics.tsx`:

```tsx
import { AlertTriangle } from "lucide-react";
import type { QueryDiagnostic } from "./parse";

// The banner is not a nicety: the parser drops unusable terms rather than
// failing, so a dropped constraint silently widens the result set. This is the
// only thing telling the user that happened, which is why it is a persistent
// block above the table rather than a toast.
export function QueryDiagnosticsBanner({ diagnostics }: { diagnostics: QueryDiagnostic[] }) {
  if (diagnostics.length === 0) return null;
  return (
    <div role="status" className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden />
      <div className="min-w-0">
        <p className="font-medium">
          {diagnostics.length === 1 ? "1 term was ignored" : `${diagnostics.length} terms were ignored`} — the results below are wider than you asked for.
        </p>
        <ul className="mt-1 space-y-0.5 text-muted-foreground">
          {diagnostics.map((d) => (
            <li key={`${d.code}:${d.term}:${d.position}`}>
              <code className="text-foreground">{d.term}</code> — {d.message}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Write the input and autocomplete**

Create `frontend/src/lib/query/QueryInput.tsx`. Use shadcn `Input` and cmdk's
`Command` (already wrapped at `src/components/ui/command.tsx`); do not hand-roll
a listbox. The core:

```tsx
import { useEffect, useRef, useState } from "react";
import { HelpCircle, Search } from "lucide-react";
import { Command, CommandEmpty, CommandGroup, CommandItem, CommandList } from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { USER_FIELDS, FIELD_TABLE } from "./fields";
import { parseQuery, type QueryDiagnostic } from "./parse";
import { resolveQuery, serializeQuery, type ResolveSource } from "./resolve";
import { QueryDiagnosticsBanner } from "./QueryDiagnostics";

/** needsQuote mirrors serializeQuery's rule, so an inserted value always parses back as one term. */
function needsQuote(v: string): boolean {
  return /[\s,"]/.test(v);
}

interface Props {
  source: ResolveSource;
  onCommit: (q: string) => void;
  diagnostics: QueryDiagnostic[];
  initialText?: string;
  compactLayout?: boolean;
}

export function QueryInput({ source, onCommit, diagnostics, initialText = "", compactLayout }: Props) {
  const [text, setText] = useState(initialText);
  const [open, setOpen] = useState(false);
  const [sheetOpen, setSheetOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // What the caret is sitting in, which decides both the suggestions and
  // whether the trailing token is still being typed.
  const caretToken = useCaretToken(text, inputRef);

  const suggestions = useMemo(() => suggestionsFor(text, caretToken, source), [text, caretToken, source]);

  const accept = (value: string) => {
    const next = replaceCaretToken(text, caretToken, value);
    setText(next);
    setOpen(false);
    inputRef.current?.focus();
  };

  // Enter always runs the query. It never accepts the highlighted suggestion:
  // the box this replaced ran on Enter, and that reflex is worth more than
  // autocomplete convenience. Tab and ArrowRight accept a suggestion.
  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      setOpen(false);
      onCommit(commitValue(text, source));
      return;
    }
    if (e.key === "Escape") {
      setOpen(false);
      return;
    }
    if ((e.key === "Tab" || e.key === "ArrowRight") && open && suggestions.length > 0) {
      e.preventDefault();
      accept(suggestions[0].insert);
    }
  };

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <Input
          ref={inputRef}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setOpen(true);
          }}
          onKeyDown={onKeyDown}
          onFocus={() => setOpen(true)}
          onBlur={() => window.setTimeout(() => setOpen(false), 150)}
          placeholder="Try: cat:Groceries amt>50 tag:vacation"
          aria-label="Transaction query"
          role="combobox"
          aria-expanded={open}
          className={cn("pl-8", compactLayout ? "h-8" : "h-9", open && suggestions.length > 0 && "rounded-b-none")}
        />
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
          <SheetTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Query syntax help" className="absolute right-1 top-1/2 h-7 w-7 -translate-y-1/2">
              <HelpCircle className="h-4 w-4" />
            </Button>
          </SheetTrigger>
          <SheetContent side="bottom">
            <SheetHeader>
              <SheetTitle>Query syntax</SheetTitle>
            </SheetHeader>
            <GrammarSheet />
          </SheetContent>
        </Sheet>
        {open && suggestions.length > 0 && (
          <div className="absolute inset-x-0 top-full z-50 rounded-b-md border bg-popover text-popover-foreground shadow-md">
            <Command>
              <CommandList>
                <CommandEmpty>No match.</CommandEmpty>
                {grouped(suggestions).map(([group, items]) => (
                  <CommandGroup key={group} heading={group}>
                    {items.map((s) => (
                      <CommandItem key={s.insert} value={s.insert} onSelect={() => accept(s.insert)}>
                        {s.label}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ))}
              </CommandList>
            </Command>
          </div>
        )}
      </div>
      <QueryDiagnosticsBanner diagnostics={diagnostics} />
    </div>
  );
}
```

Then add the three helpers it references, in the same file:

```tsx
interface CaretToken {
  start: number;
  text: string;
  /** True when the token runs to the end of the input, i.e. still being typed. */
  atEnd: boolean;
  /** The field name when the token is `field:` or `field<op>`. */
  field: string | null;
}

function useCaretToken(text: string, ref: React.RefObject<HTMLInputElement | null>): CaretToken {
  const caret = ref.current?.selectionStart ?? text.length;
  let start = caret;
  while (start > 0 && !/[\s]/.test(text[start - 1])) start--;
  const raw = text.slice(start, caret);
  const colon = raw.indexOf(":");
  const opMatch = /^([a-z]+)(>=|<=|!=|~|>|<|=)/.exec(raw);
  return {
    start,
    text: raw,
    atEnd: caret >= text.length,
    field: colon > 0 ? raw.slice(0, colon) : opMatch ? opMatch[1] : null,
  };
}

interface Suggestion {
  group: string;
  label: string;
  /** Exactly what is written into the input — quoted when it must be. */
  insert: string;
}

function suggestionsFor(text: string, token: CaretToken, source: ResolveSource): Suggestion[] {
  const out: Suggestion[] = [];
  if (!token.field) {
    // A fresh token: offer the field names, and keep any plain text the user
    // already typed as an additional plain-search term.
    for (const f of USER_FIELDS) {
      if (f.startsWith(token.text.toLowerCase())) out.push({ group: "Fields", label: f, insert: `${f}:` });
    }
    return out;
  }
  const def = FIELD_TABLE[token.field];
  if (!def) return out;
  const partial = token.text.slice(token.field.length + (token.text.includes(":") ? 1 : 0));
  const label = partial.replace(/^[:=<>!~]+/, "").toLowerCase();
  if (def.enum) {
    for (const v of def.enum) {
      if (v.startsWith(label)) out.push({ group: token.field, label: v, insert: `${token.field}:${v}` });
    }
    return out;
  }
  const value = (v: string) => (needsQuote(v) ? `"${v.replace(/(["\\])/g, "\\$1")}"` : v);
  if (token.field === "acct") {
    for (const a of source.accounts) {
      if (a.name.toLowerCase().includes(label)) out.push({ group: "Accounts", label: a.name, insert: `acct:${value(a.name)}` });
    }
  }
  if (token.field === "payee") {
    for (const p of source.payees) {
      if (p.name.toLowerCase().includes(label)) out.push({ group: "Payees", label: p.name, insert: `payee:${value(p.name)}` });
    }
  }
  if (token.field === "tag") {
    for (const t of source.tags) {
      if (t.toLowerCase().includes(label)) out.push({ group: "Tags", label: t, insert: `tag:${value(t)}` });
    }
  }
  if (token.field === "cat" || token.field === "group") {
    const groupName = (id: string) => source.groups.find((g) => g.id === id)?.name ?? "";
    const ambiguous = new Set<string>();
    for (const c of source.categories) {
      if (!c.name.toLowerCase().includes(label)) continue;
      const qualified = `${groupName(c.groupId)}/${c.name}`;
      if (source.categories.filter((o) => o.name.toLowerCase() === c.name.toLowerCase()).length > 1) ambiguous.add(c.name.toLowerCase());
      out.push({ group: "Categories", label: qualified, insert: `${token.field}:${value(qualified)}` });
    }
    for (const g of source.groups) {
      if (g.name.toLowerCase().includes(label)) out.push({ group: "Groups", label: g.name, insert: `group:${value(g.name)}` });
    }
    void ambiguous;
  }
  if (token.field === "date") {
    for (const p of PERIOD_VALUES) {
      if (p.includes(label)) out.push({ group: "Periods", label: p, insert: `date:${value(p)}` });
    }
  }
  return out;
}

function replaceCaretToken(text: string, token: CaretToken, value: string): string {
  return text.slice(0, token.start) + value + text.slice(token.start + token.text.length);
}

// commitValue is the value Enter sends: the same parse-and-resolve the hook
// does, so what is committed is exactly what the box is showing.
function commitValue(text: string, source: ResolveSource): string {
  return serializeQuery(resolveQuery(parseQuery(text), source).terms);
}

function grouped(items: Suggestion[]): Array<[string, Suggestion[]]> {
  const map = new Map<string, Suggestion[]>();
  for (const i of items) map.set(i.group, [...(map.get(i.group) ?? []), i]);
  return [...map.entries()];
}
```

`suggestionsFor` also needs `PERIOD_VALUES`, imported from `@/lib/dates` alongside
`periodRange` in Task 7:

```ts
import { PERIOD_VALUES } from "@/lib/dates";
```

Delete the `ambiguous` set and its `void ambiguous` line from the `cat`/`group`
branch: the `Food/Groceries` label already disambiguates every ambiguous
suggestion, so the set has no reader.

Then add `GrammarSheet`, which renders the same field table the suggestions come
from, so the documented syntax cannot drift from the accepted syntax:

```tsx
function GrammarSheet() {
  return (
    <div className="space-y-4 px-4 pb-6 text-sm">
      <p className="text-muted-foreground">
        Terms are combined with AND. A comma-separated value matches any of them. Prefix a term with <code>not</code> to
        exclude it. A bare word is a free-text search.
      </p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        {USER_FIELDS.map((f) => (
          <div key={f} className="contents">
            <dt><code>{f}:</code></dt>
            <dd className="text-muted-foreground">{describe(FIELD_TABLE[f])}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function describe(def: (typeof FIELD_TABLE)[string]): string {
  if (def.enum) return def.enum.join(" | ");
  switch (def.kind) {
    case "uuid":
      return "an id, or none / uncategorized";
    case "amount":
      return "a decimal in major units; supports > >= < <= = !=";
    case "date":
      return "YYYY-MM-DD or a named period; supports > >= < <= = !=";
    case "tags":
      return "a tag name, comma-separated for any of";
    default:
      return "text; ~ is contains";
  }
}
```

- [ ] **Step 5: Run the input tests**

Run: `cd frontend; bun run test src/lib/query/QueryInput.test.tsx`
Expected: PASS. If the popup will not open, check the pinned `jsdom` version —
`AGENTS.md` records that Radix popups stop opening after an open/close cycle on
jsdom 30.1.0, and `~30.0.1` is deliberate. Do not relax the pin; re-run the full
suite first.

- [ ] **Step 6: Swap the input into the filter bar**

In `transactionConstants.ts`, replace `"search"` in `URL_PARAMS` with `"q"`, and
in `DEFAULT_FILTERS` replace `search: ""` with `q: ""`.

In `TransactionFilters.tsx`, remove the `search` `Input` (lines ~147-156) and
render the query input in its place:

```tsx
<QueryInput
  source={{ accounts, categories, groups, payees, tags: tags.map((t) => t.name) }}
  onCommit={(q) => onFilterChange("q", q)}
  diagnostics={queryDiagnostics}
  compactLayout={compactLayout}
/>
```

`TransactionFilters` needs the reference data it does not currently take. Add
`categories`, `groups` and `tags` to its props and pass them from
`Transactions.tsx`, which already has all of them (the categories/groups from
`useDomainData()`, the tags from its own `api.getTags()` at `Transactions.tsx:343`).

- [ ] **Step 7: Keep the export and the bulk selection in step**

In `Transactions.tsx`, `handleExport` copies `filters` and deletes
`page`/`limit`/`sortBy`/`sortOrder`. Because `q` is now a `URL_PARAMS` key it is
copied automatically, so the CSV export honours the query with no change. Add a
test asserting that: with `q=amt>50` in the filters, `exportTransactions` is
called with `q` present.

- [ ] **Step 8: Run the Transactions suite**

Run: `cd frontend; bun run test src/components/Transactions/ src/lib/query/`
Expected: PASS, including the pre-existing `TransactionFilters.test.tsx`. Update
its assertions from `search` to `q` rather than deleting them.

- [ ] **Step 9: Commit**

```bash
git add frontend/src
git commit -m "feat(transactions): the query input, autocomplete and ignored-terms banner"
```

---

### Task 10: Full verification gate

**Files:** none — this task runs the gates and fixes whatever they surface.

- [ ] **Step 1: Backend suite, vet and coverage floor**

Run: `make test-cover-check vet`
Expected: PASS. If coverage falls below 85%, the shortfall is almost certainly
`query/compile.go`'s per-field emitters — add table cases for `group`, `tag`,
`recurring` and the `!=` operator rather than excluding the file.

- [ ] **Step 2: Client, TUI and MCP modules**

Run: `make test-client-cover-check test-tui-cover-check test-mcp-cover-check vet-client vet-tui vet-mcp`
Expected: PASS.

- [ ] **Step 3: Spec parity and docs**

Run: `make openapi-check docs-check`
Expected: PASS.

- [ ] **Step 4: Frontend gates**

Run: `cd frontend; bun run typecheck && bun run test:coverage && bun run build`
Expected: PASS. `test:coverage` enforces the v8 thresholds in `vitest.config.ts`
(statements 76, branches 67, functions 71, lines 78); the new `lib/query` code
is well covered, but if the total dips, the autocomplete branches in
`QueryInput.tsx` are where to add cases.

- [ ] **Step 5: Integration tests**

Run: `make test-integration`
Expected: PASS. If Docker is unavailable, say so explicitly in the summary
rather than reporting the gate as met.

- [ ] **Step 6: Tidy check**

Run: `cd backend; go mod tidy; cd ..; git diff --exit-code backend/go.mod backend/go.sum client/go.sum mcp/go.sum tui/go.sum`
Expected: no output. CI enforces this, so a dirty `go.mod` fails the release gate
even though nothing here changed a dependency.

- [ ] **Step 7: Manual verification against a real stack**

Run: `make dev`, then exercise the feature end to end in the browser:
- `cat:<a real category> amt>50` — the count agrees with the rows, and the
  amount filter is in dollars.
- `catgory:food` — a 200 with **every** transaction and a visible banner.
- A query that also has a filter-bar selection — the two intersect.
- An offline reload with a query in the URL — the previous view loads, and the
  cache did not grow an entry per query.
- Type half a field name, slowly — the banner must not strobe.

- [ ] **Step 8: Commit any fixes the gates surfaced**

```bash
git add -A
git commit -m "test(query): close the coverage gaps the gates surfaced"
```

---

## Self-Review Notes

**Spec coverage.** Every section of the spec maps to a task: grammar and field
table (1, 2), the in-progress rule (8, 9), autocomplete (9), resolution (7),
leniency and diagnostics (1, 3, 8, 9), the compiler-into-`txnFilter` decision
(2, 3), offline cache (8), parity (4, 5, 6), testing (1, 2, 6, 7, 8, 9), risks
(1 for the date window, 2 for the money conversion, 3 for AND-semantics, 9 for
the UI), and the deferred surfaces (no task — intentionally out of scope).

**Two deliberate deviations from the spec, both recorded above.** The corpus
carries the grammar contract plus a Go-only `sql` key rather than being a single
combined fixture, so the frontend suite is not asserting SQL it cannot produce.
And `Source`/amount handling was re-checked against the real code: the list
response is an inline `gin.H` (not a `models` struct) and the offline cache is
keyed on the full URL, so the spec's original claims about both were corrected
before this plan was written.