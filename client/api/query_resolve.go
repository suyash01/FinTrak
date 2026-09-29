package api

import (
	"fmt"
	"strings"
)

// The `q` expression names things the way a person would ("cat:Groceries") while
// the server resolves no names at all: it binds a uuid or it drops the term and
// says so. ResolveQuery is the step in between, and it belongs to the caller
// rather than the server for the same reason the web app performs it in
// frontend/src/lib/query/resolve.ts — the server has no business knowing what a
// user calls their categories, and the diagnostic for a name that matches nothing
// is only useful if it can say which names the user actually has.
//
// It is pure: the reference data arrives as a RefSource and nothing here touches
// the network, a clock or a database. That is what makes it testable, and it is
// why the TUI can call it while composing a filter rather than after a response.

// RefSource is the user's own reference data, which is all a name can be
// resolved against. The four lists are the ones the API already returns and the
// clients already hold; there is no tag list because a tag is already a name.
type RefSource struct {
	Accounts   []Account
	Categories []Category
	Groups     []CategoryGroup
	Payees     []Payee
}

// idFields are the four `q` fields whose value is a row in the user's own data,
// and so the only ones worth resolving. Every other field is already in its
// final wire form: an amount stays in major units for the server to convert, a
// currency code is its own name, and a tag has no table to look up.
var idFields = map[string]bool{"cat": true, "group": true, "acct": true, "payee": true}

// nullSentinels are the two words that stand for an absent value. They are not
// names, so they are never looked up — and on a field whose column is nullable
// the server turns one into IS NULL, which no name could ever mean.
var nullSentinels = map[string]bool{"none": true, "uncategorized": true}

// ResolveQuery rewrites the names in a `q` expression into the ids the server
// can act on, returning the expression to send and a diagnostic for every term
// that could not be used.
//
// It never returns an error. A term it cannot resolve is dropped and reported
// rather than passed on as a literal: a name bound against a uuid column comes
// back as an empty ledger with nothing to explain it, and a term that resolves
// to nothing must be a no-op rather than a predicate that silently matches
// nothing. The diagnostics are the same QueryDiagnostic values the server
// reports, so a caller has one shape to render either way.
//
// A name matching more than one row is kept as all of them, with an
// ambiguous_value diagnostic. Narrowing to one would be a guess, and the user
// would read the narrower result as the answer they asked for.
func ResolveQuery(q string, src RefSource) (string, []QueryDiagnostic) {
	if strings.TrimSpace(q) == "" {
		return q, nil
	}

	runes := []rune(q)
	var (
		out   strings.Builder
		diags []QueryDiagnostic
	)
	i := 0
	for i < len(runes) {
		term, start, end, ok := scanTerm(runes, i)
		if !ok {
			// A character that cannot start a token. Copy it so the output stays
			// byte-for-byte what the user typed, and advance so one byte of
			// unparseable input cannot loop forever.
			out.WriteRune(runes[i])
			i++
			continue
		}
		// The gap before the term is copied whatever happens to the term, so a
		// dropped one takes its own separator with it and the next term is not
		// welded onto this one's text.
		out.WriteString(string(runes[i:start]))
		if !idFields[term.field] {
			// Everything else is already in wire form and is copied verbatim, so a
			// term this resolver does not understand cannot be altered by it.
			out.WriteString(string(runes[start:end]))
			i = end
			continue
		}

		resolved, termDiags := resolveTerm(term, src)
		diags = append(diags, termDiags...)
		if resolved != "" {
			out.WriteString(resolved)
		}
		i = end
	}

	return strings.TrimSpace(out.String()), diags
}

// scannedTerm is one token of a `q` expression, as far as this resolver reads it.
type scannedTerm struct {
	field    string
	values   []string
	negated  bool
	position int
}

// scanTerm reads one term starting at i and returns the index just past it. The
// tokenizer mirrors backend/internal/query/parse.go — the same word, operator and
// value rules — because this resolver has to find the same term boundaries the
// server will, or it would rewrite something the server was not going to read as
// a field at all.
func scanTerm(runes []rune, i int) (scannedTerm, int, int, bool) {
	n := len(runes)
	for i < n && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	if i >= n {
		return scannedTerm{}, i, i, false
	}
	// The term's own start, which is what the caller copies the gap up to.
	start := i

	term := scannedTerm{position: start}
	if word, next := readWord(runes, i); strings.EqualFold(word, "not") {
		term.negated = true
		i = next
		for i < n && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
	}

	word, after := readWord(runes, i)
	if word != "" && after < n && runes[after] == ':' {
		// `not cat:` with nothing after it is a missing value, not a name that
		// matched nothing. Reading one anyway would produce a single empty value,
		// which resolves to no category and the whole term would be dropped here —
		// silently, and with a diagnostic blaming a name the user never typed. The
		// term is copied verbatim instead and the server reports it properly.
		j := after + 1
		for j < n && (runes[j] == ' ' || runes[j] == '\t') {
			j++
		}
		if j >= n {
			return scannedTerm{}, start, n, true
		}
		end, values := readValue(runes, after+1)
		if len(values) == 0 {
			return scannedTerm{}, start, end, true
		}
		term.field = strings.ToLower(word)
		term.values = values
		return term, start, end, true
	}
	if word != "" {
		if op, width, ok := matchOp(runes, after); ok {
			end, values := readValue(runes, after+width)
			// Only the `=` spelling is resolved. A comparison against a name has no
			// meaning for an id field — `cat!=Groceries` is not a request the server
			// can answer — so such a term keeps an empty field here, is copied
			// verbatim, and the server reports it.
			if op == "=" && len(values) > 0 {
				term.field = strings.ToLower(word)
				term.values = values
			}
			return term, start, end, true
		}
	}

	// A bare-word run: plain search. It has no field to resolve, so this only has
	// to find where it ends.
	j := i
	for j < n && runes[j] != ' ' && runes[j] != '\t' {
		j++
	}
	return scannedTerm{}, start, j, true
}

// readWord reads the run of characters at i that cannot be part of a field name.
func readWord(runes []rune, i int) (string, int) {
	n := len(runes)
	j := i
	for j < n {
		r := runes[j]
		if r == ' ' || r == '\t' || r == '"' || r == ',' || r == ':' || isOpRune(r) {
			break
		}
		j++
	}
	return string(runes[i:j]), j
}

// matchOp reports the operator at i and its width, longest spelling first so
// `>=` is not read as `>` followed by a value.
func matchOp(runes []rune, i int) (string, int, bool) {
	rest := string(runes[i:])
	for _, cand := range []string{">=", "<=", "!=", "~", ">", "<", "="} {
		if strings.HasPrefix(rest, cand) {
			return cand, len(cand), true
		}
	}
	return "", 0, false
}

func isOpRune(r rune) bool {
	return r == '>' || r == '<' || r == '=' || r == '!' || r == '~'
}

// readValue reads one value at i — quoted, or a bare comma-separated run — and
// returns the index just past it.
func readValue(runes []rune, i int) (int, []string) {
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
	var values []string
	for _, part := range strings.Split(string(runes[i:j]), ",") {
		if p := strings.TrimSpace(part); p != "" {
			values = append(values, p)
		}
	}
	if len(values) == 0 {
		// A run of only commas is kept as one literal item, so the term is
		// reported rather than vanishing with nothing to show for it.
		return j, []string{string(runes[i:j])}
	}
	return j, values
}

// resolveTerm turns one id-field term into its wire form, or "" when nothing in
// it could be resolved — in which case it must not be sent.
func resolveTerm(term scannedTerm, src RefSource) (string, []QueryDiagnostic) {
	var (
		resolved  []string
		unmatched []string
		ambiguous []string
	)
	for _, v := range term.values {
		if nullSentinels[v] || isUUIDValue(v) {
			resolved = append(resolved, v)
			continue
		}
		hits := lookup(term.field, v, src)
		switch len(hits) {
		case 0:
			unmatched = append(unmatched, v)
		case 1:
			resolved = append(resolved, hits[0])
		default:
			ambiguous = append(ambiguous, v)
			resolved = append(resolved, hits...)
		}
	}

	var diags []QueryDiagnostic
	for _, v := range unmatched {
		diags = append(diags, QueryDiagnostic{
			Term:     term.field + ":" + v,
			Code:     "unresolved_value",
			Message:  fmt.Sprintf("no %s named %q", labelFor(term.field), v),
			Position: term.position,
		})
	}
	if len(ambiguous) > 0 {
		// The advice has to name a spelling this field actually understands. Only
		// cat resolves `Group/Name`; telling someone to disambiguate an account
		// that way sends them to a string that resolves to nothing.
		hint := " Use the exact name."
		switch term.field {
		case "cat":
			hint = " Use cat:Group/Name to pick one."
		case "group":
			hint = " Use the group's exact name."
		}
		diags = append(diags, QueryDiagnostic{
			Term:     term.field + ":" + strings.Join(ambiguous, ","),
			Code:     "ambiguous_value",
			Message:  fmt.Sprintf("%q matched more than one %s; keeping all of them.%s", strings.Join(ambiguous, ", "), labelFor(term.field), hint),
			Position: term.position,
		})
	}

	// A term whose every value failed to resolve emits nothing, so the filter is
	// a no-op rather than a false predicate that would silently empty the list.
	if len(resolved) == 0 {
		return "", diags
	}
	body := term.field + ":" + strings.Join(resolved, ",")
	if term.negated {
		return "not " + body, diags
	}
	return body, diags
}

// lookup resolves one written value against the user's own data, accepting the
// `Group/Name` qualified spelling for a category. Matching is case-insensitive
// because a person types "whole foods" for "Whole Foods".
func lookup(field, raw string, src RefSource) []string {
	slash := strings.Index(raw, "/")
	qualifier, name := "", raw
	if slash > 0 {
		qualifier, name = strings.ToLower(raw[:slash]), raw[slash+1:]
	}
	name = strings.ToLower(name)

	switch field {
	case "cat":
		groupID := ""
		if qualifier != "" {
			for _, g := range src.Groups {
				if strings.ToLower(g.Name) == qualifier {
					groupID = g.ID
					break
				}
			}
		}
		var ids []string
		for _, c := range src.Categories {
			if strings.ToLower(c.Name) == name && (groupID == "" || c.GroupID == groupID) {
				ids = append(ids, c.ID)
			}
		}
		return ids
	case "group":
		for _, g := range src.Groups {
			if strings.EqualFold(g.ID, raw) {
				return []string{g.ID}
			}
		}
		var ids []string
		for _, g := range src.Groups {
			if strings.ToLower(g.Name) == name {
				ids = append(ids, g.ID)
			}
		}
		return ids
	case "acct":
		var ids []string
		for _, a := range src.Accounts {
			if strings.ToLower(a.Name) == name {
				ids = append(ids, a.ID)
			}
		}
		return ids
	case "payee":
		var ids []string
		for _, p := range src.Payees {
			if strings.ToLower(p.Name) == name {
				ids = append(ids, p.ID)
			}
		}
		return ids
	}
	return nil
}

// labelFor names the thing a field points at, for a message a person reads.
func labelFor(field string) string {
	switch field {
	case "cat":
		return "category"
	case "group":
		return "category group"
	case "acct":
		return "account"
	case "payee":
		return "payee"
	default:
		return field
	}
}

// isUUIDValue is deliberately shape-only: it decides whether a value is already
// an id to pass through, not whether it is one this user owns.
func isUUIDValue(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHexDigit(c) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
