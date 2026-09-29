package ui

import "github.com/fintrak/client/api"

// resolveQuery turns the names in a `q` expression into the ids the server can
// act on, against the reference data this session already loaded.
//
// It is a thin adapter over api.ResolveQuery rather than a second implementation:
// the resolution rules — which fields take a name, the `Group/Name` qualifier, the
// drop-and-report behaviour — are the shared client's, so a caller that does not
// go through the API client (this one) and one that does cannot drift on what
// `cat:Groceries` means. The web app performs the same step in
// frontend/src/lib/query/resolve.ts, and the two are pinned to the same cases.
//
// A nil RefData resolves nothing, which drops every name and reports it. That is
// the right answer rather than a silent pass-through: the server would drop the
// same term anyway, and only it could say why — but a name reaching the server
// unresolved binds a string against a uuid column, so it is not a harmless no-op.
func resolveQuery(q string, ref *RefData) (string, []api.QueryDiagnostic) {
	if ref == nil {
		ref = &RefData{}
	}
	return api.ResolveQuery(q, api.RefSource{
		Accounts:   ref.Accounts,
		Categories: ref.Categories,
		Groups:     ref.Groups,
		Payees:     ref.Payees,
	})
}
