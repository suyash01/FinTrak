// Package query parses the `q` parameter of GET /transactions: a small
// expression language a power user can type into the transaction search box.
//
// The parser is lenient by design. Nothing here returns an error: a term it
// cannot understand is dropped and reported, because a dropped constraint
// silently widens a result set and the user would read the whole ledger as if it
// were the answer. Every dropped term therefore comes back as a Diagnostic, and
// the handler puts those in the response so the UI can say what it ignored.
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

// The comparison operators, longest first so ">=" wins over ">".
const (
	OpEq   Op = "="
	OpNe   Op = "!="
	OpGt   Op = ">"
	OpGe   Op = ">="
	OpLt   Op = "<"
	OpLe   Op = "<="
	OpLike Op = "~"
)

// FieldPlain is the pseudo-field a bare word compiles to: the existing 4-way OR
// over description, notes, payee name and tags. The parser synthesises it and
// never accepts it as user input, so `plain:coffee` is an unknown field.
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

var operators = []struct {
	sym string
	op  Op
}{
	{">=", OpGe}, {"<=", OpLe}, {"!=", OpNe}, {"~", OpLike},
	{">", OpGt}, {"<", OpLt}, {"=", OpEq},
}

// isOperatorRune reports whether r can begin or continue an operator. Used to
// spot a doubled operator such as `amt>>50`, which is a typo rather than a
// comparison.
func isOperatorRune(r rune) bool {
	switch r {
	case '>', '<', '=', '!', '~':
		return true
	}
	return false
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

		if len(expr.Terms) >= MaxTerms {
			diags = append(diags, Diagnostic{
				Term: strings.TrimSpace(string(runes[i:])), Code: CodeTooLong,
				Message: "query has more than 32 terms; the rest was ignored",
			})
			return expr, diags
		}

		// start is the offset of the term as the user wrote it, so a `not`
		// prefix is inside it: the UI highlights "not cat:a,b", not "cat:a,b".
		start := i

		// `not` prefix.
		negated := false
		if word, next := readWord(runes, i); strings.EqualFold(word, "not") {
			negated = true
			i = next
			skipSpaces()
		}

		word, after := readWord(runes, i)

		// `field:value`
		if word != "" && after < n && runes[after] == ':' {
			field := strings.ToLower(word)
			def, known := userField(field)
			i = after + 1
			skipSpaces()
			if i >= n {
				diags = append(diags, Diagnostic{
					Term: string(runes[start : after+1]), Code: CodeMissingValue,
					Message: field + ": needs a value", Position: start,
				})
				continue
			}
			end, values := readValue(runes, i)
			i = end
			raw := string(runes[start:end])
			switch {
			case !known:
				diags = append(diags, Diagnostic{
					Term: raw, Code: CodeUnknownField,
					Message: "unknown field " + field, Position: start,
				})
			default:
				term := Term{Field: field, Op: OpEq, Values: values, Negated: negated, Position: start, Raw: raw}
				if err := def.validate(term); err != nil {
					diags = append(diags, toDiagnostic(err, raw, start))
					continue
				}
				expr.Terms = append(expr.Terms, term)
			}
			continue
		}

		// `field<op>value`. The operator sits just past the field name, which is
		// where readWord stopped. Matching at i would never fire, because i is
		// the start of the name, not the operator.
		//
		// The word must be non-empty: at a bare operator character there is no
		// field, and reporting "unknown field " for it would be noise.
		if word != "" {
			if op, width, ok := matchOp(runes, after); ok {
				name := strings.ToLower(word)
				afterOp := after + width

				// A doubled operator (`amt>>50`) is a typo, not a comparison: the
				// first operator matched and the next character is another one.
				// The whole malformed token is consumed, so nothing after the
				// typo leaks through as a stray bare word.
				if afterOp < n && isOperatorRune(runes[afterOp]) {
					end := afterOp + 1
					for end < n && runes[end] != ' ' && runes[end] != '\t' {
						end++
					}
					diags = append(diags, Diagnostic{
						Term: string(runes[start:end]), Code: CodeBadOperator,
						Message: "malformed operator after " + name, Position: start,
					})
					i = end
					continue
				}

				def, known := userField(name)
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
				i = end
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
						diags = append(diags, toDiagnostic(err, raw, start))
						continue
					}
					expr.Terms = append(expr.Terms, term)
				}
				continue
			}
		}

		// A bare word: plain search. Collect words until the next `field:` or
		// `field<op>` shape, so "coffee shop" is one term.
		var words []string
		j := i
		for j < n {
			for j < n && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			if j >= n {
				break
			}
			w, next := readWord(runes, j)
			if w == "" {
				j++
				continue
			}
			if next < n && runes[next] == ':' {
				break
			}
			if _, _, isOp := matchOp(runes, next); isOp {
				break
			}
			words = append(words, w)
			j = next
		}
		if len(words) == 0 {
			// A character we cannot start a token with (a stray quote or comma).
			// Skip it so one byte cannot loop forever.
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

// readWord reads a run of characters starting at i that cannot be part of a
// field name, returning the word and the index just past it. An empty word means
// i sits on a delimiter.
//
// It stops at ':' because that introduces a field, and at the operator runes
// because they follow one. It does NOT stop at a space inside a quoted value —
// readValue handles quotes separately, and only a field name or a bare word
// ever reaches here.
func readWord(runes []rune, i int) (word string, next int) {
	n := len(runes)
	j := i
	for j < n {
		r := runes[j]
		if r == ' ' || r == '\t' || r == '"' || r == ',' || r == ':' {
			break
		}
		if isOperatorRune(r) {
			break
		}
		j++
	}
	return string(runes[i:j]), j
}

// matchOp reports the operator starting at i, if any, and its width.
func matchOp(runes []rune, i int) (Op, int, bool) {
	rest := string(runes[i:])
	for _, cand := range operators {
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
		// A run of only commas: keep it as one literal item, so the term is
		// reported as unresolvable rather than silently matching nothing.
		return j, []string{string(runes[i:j])}
	}
	return j, values
}
