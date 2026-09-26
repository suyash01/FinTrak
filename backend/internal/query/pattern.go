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
