package handlers

import "github.com/fintrak/backend/internal/query"

// txnFilterSink adapts txnFilter to query.Sink. It exists so the query package
// never imports handlers, and so txnFilter keeps its existing unexported method
// names — the methods below are exercised by many pgxmock tests that assert
// exact SQL strings and argument order.
type txnFilterSink struct{ f *txnFilter }

func (s txnFilterSink) Param(clause string, value any) { s.f.param(clause, value) }
func (s txnFilterSink) AnyOf(clauses []string)         { s.f.anyOf(clauses) }
func (s txnFilterSink) Params(clause string, v ...any) { s.f.params(clause, v...) }
func (s txnFilterSink) Raw(clause string)              { s.f.raw(clause) }

func (s txnFilterSink) Clause(format string, value any) string {
	return s.f.clause(format, value)
}

// compileQuery parses and compiles the q= parameter onto f, returning a
// diagnostic for every term it dropped.
//
// It must be called LAST in txnQueryFilter, after every other clause has been
// appended: the compiler continues the placeholder numbering from
// len(f.args), and args[0] is always the user id. Appending it earlier would
// renumber the existing parameters and break every test that pins their order.
func compileQuery(q string, f *txnFilter) []query.Diagnostic {
	if q == "" {
		return nil
	}
	expr, diags := query.Parse(q)
	diags = append(diags, query.Compile(expr, txnFilterSink{f})...)
	return diags
}
