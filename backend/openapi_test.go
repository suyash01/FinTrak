package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

// openAPIDocument is the minimal shape of openapi.yaml needed to guard route
// coverage. Method blocks are decoded as raw maps so we only care about keys.
type openAPIDocument struct {
	OpenAPI string                            `yaml:"openapi"`
	Paths   map[string]map[string]interface{} `yaml:"paths"`
}

// ginParamRE converts Gin's ":id" path params to OpenAPI's "{id}".
var ginParamRE = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// openAPIPath maps a registered Gin route to its openapi.yaml path key (the
// spec paths are relative to the /api/v1 server base).
func openAPIPath(ginPath string) string {
	p := strings.TrimPrefix(ginPath, "/api/v1")
	return ginParamRE.ReplaceAllString(p, `{$1}`)
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "options": true, "head": true,
}

// schemaProperties is the component-schemas half of the document: the property
// names of each schema, which is all the model comparison needs.
type schemaProperties struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				Type string `yaml:"type"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// schemaExceptions lists property differences that are deliberate, per schema.
// It is empty: every model-backed schema and its struct agree.
var schemaExceptions = map[string][]string{}

// TestOpenAPISchemasMatchTheModels compares every component schema that shares a
// name with a struct in models.go against that struct's JSON fields — including
// the ones it inherits from an embedded struct, since those are what the encoder
// writes.
//
// Route parity proves the paths exist; nothing else notices when a *schema*
// drifts from the type the handlers actually serialize. That is how the
// Transaction schema came to advertise linkCount/linkId (fields no code has ever
// sent) and Category a "type" it does not have, while both omitted real fields.
//
// A schema with no same-named struct is not compared: request bodies and the
// handler-built dashboard payloads are hand-written. A deliberate difference
// belongs in schemaExceptions with a reason.
func TestOpenAPISchemasMatchTheModels(t *testing.T) {
	var doc schemaProperties
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))
	require.NotEmpty(t, doc.Components.Schemas, "openapi.yaml has no component schemas")

	models := modelJSONFields(t)
	require.NotEmpty(t, models, "no structs parsed from models.go")

	moneyFields := modelMoneyJSONFields(t)

	compared := 0
	for name, schema := range doc.Components.Schemas {
		fields, ok := models[name]
		if !ok {
			continue // hand-written schema: a request DTO or a handler payload
		}
		compared++

		want := map[string]bool{}
		for _, f := range fields {
			want[f] = true
		}
		got := map[string]bool{}
		for prop := range schema.Properties {
			got[prop] = true
		}
		for _, ex := range schemaExceptions[name] {
			delete(want, ex)
			delete(got, ex)
		}

		var missing, extra []string
		for f := range want {
			if !got[f] {
				missing = append(missing, f)
			}
		}
		for f := range got {
			if !want[f] {
				extra = append(extra, f)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		assert.Emptyf(t, missing,
			"%s: models.go has fields the schema omits: %v (document them, or list them in schemaExceptions with a reason)", name, missing)
		assert.Emptyf(t, extra,
			"%s: the schema advertises fields no model has: %v (remove them, or list them in schemaExceptions with a reason)", name, extra)

		// A money.Amount marshals as a bare JSON number in decimal major units
		// (money.Amount.MarshalJSON), so every schema property backed by one
		// must be `number` — documenting it as `integer` (the raw minor-unit
		// column) is how the backup loan schemas came to promise cents while
		// the encoder sent dollars. Names alone cannot catch that.
		for _, f := range moneyFields[name] {
			if _, ok := schema.Properties[f]; !ok {
				continue // already reported as missing above
			}
			assert.Equalf(t, "number", schema.Properties[f].Type,
				"%s.%s is a money.Amount (decimal major units on the wire) but the schema says type: %q",
				name, f, schema.Properties[f].Type)
		}
	}

	require.Greater(t, compared, 50,
		"the comparison found suspiciously few model-backed schemas — did the models or the spec move?")
}

// modelJSONFields parses models.go and returns each struct's JSON field names,
// expanding embedded structs because their fields are part of the JSON too.
func modelJSONFields(t *testing.T) map[string][]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "models/models.go", nil, 0)
	require.NoError(t, err)

	structs := map[string]*ast.StructType{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			structs[ts.Name.Name] = st
		}
		return true
	})

	var collect func(name string, seen map[string]bool) []string
	collect = func(name string, seen map[string]bool) []string {
		st, ok := structs[name]
		if !ok || seen[name] {
			return nil
		}
		seen[name] = true

		var out []string
		for _, field := range st.Fields.List {
			if field.Tag != nil {
				tag, ok := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Lookup("json")
				if !ok {
					continue
				}
				if f := strings.Split(tag, ",")[0]; f != "" && f != "-" {
					out = append(out, f)
				}
				continue
			}
			// An untagged field is an embedded struct.
			if len(field.Names) == 0 {
				if id, ok := field.Type.(*ast.Ident); ok {
					out = append(out, collect(id.Name, seen)...)
				}
			}
		}
		return out
	}

	out := make(map[string][]string, len(structs))
	for name := range structs {
		out[name] = collect(name, map[string]bool{})
	}
	return out
}

// modelMoneyJSONFields parses models.go and returns, per struct, the JSON field
// names whose Go type is money.Amount (or *money.Amount). Those fields are the
// ones the schema must document as `number`, so the assertion above can catch a
// money field documented as the raw minor-unit integer.
func modelMoneyJSONFields(t *testing.T) map[string][]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "models/models.go", nil, 0)
	require.NoError(t, err)

	structs := map[string]*ast.StructType{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			structs[ts.Name.Name] = st
		}
		return true
	})

	var isMoney func(expr ast.Expr) bool
	isMoney = func(expr ast.Expr) bool {
		switch e := expr.(type) {
		case *ast.SelectorExpr:
			if id, ok := e.X.(*ast.Ident); ok {
				return id.Name == "money" && e.Sel.Name == "Amount"
			}
		case *ast.StarExpr:
			return isMoney(e.X)
		}
		return false
	}

	var collect func(name string, seen map[string]bool) []string
	collect = func(name string, seen map[string]bool) []string {
		st, ok := structs[name]
		if !ok || seen[name] {
			return nil
		}
		seen[name] = true

		var out []string
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 {
				if id, ok := field.Type.(*ast.Ident); ok {
					out = append(out, collect(id.Name, seen)...)
				}
				continue
			}
			if !isMoney(field.Type) || field.Tag == nil {
				continue
			}
			tag, ok := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Lookup("json")
			if !ok {
				continue
			}
			if f := strings.Split(tag, ",")[0]; f != "" && f != "-" {
				out = append(out, f)
			}
		}
		return out
	}

	out := make(map[string][]string, len(structs))
	for name := range structs {
		out[name] = collect(name, map[string]bool{})
	}
	return out
}

// TestOpenAPICoversRegisteredRoutes fails when a handler is added or renamed
// without updating openapi.yaml.
func TestOpenAPICoversRegisteredRoutes(t *testing.T) {
	var doc openAPIDocument
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))
	require.NotEmpty(t, doc.OpenAPI, "openapi.yaml is missing the openapi version")
	require.NotEmpty(t, doc.Paths, "openapi.yaml has no paths")

	var missing []string
	for _, route := range testRouter().Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		key := openAPIPath(route.Path)
		methods, ok := doc.Paths[key]
		if !ok {
			missing = append(missing, route.Method+" "+route.Path)
			continue
		}
		if _, ok = methods[strings.ToLower(route.Method)]; !ok {
			missing = append(missing, route.Method+" "+route.Path)
		}
	}
	assert.Emptyf(t, missing, "routes missing from openapi.yaml: %v", missing)
}

// TestOpenAPIDocumentsOnlyRegisteredRoutes fails when the spec advertises an
// endpoint that no longer exists.
func TestOpenAPIDocumentsOnlyRegisteredRoutes(t *testing.T) {
	var doc openAPIDocument
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))

	registered := make(map[string]map[string]bool)
	for _, route := range testRouter().Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		key := openAPIPath(route.Path)
		if registered[key] == nil {
			registered[key] = map[string]bool{}
		}
		registered[key][strings.ToLower(route.Method)] = true
	}

	for path, methods := range doc.Paths {
		for method := range methods {
			if !httpMethods[method] {
				continue
			}
			require.Truef(t, registered[path][method],
				"openapi.yaml documents %s %s but no matching route is registered",
				strings.ToUpper(method), path)
		}
	}
}

// suppressedCyclesDoc reaches one description: the MoneyFlowSuppressedCycle
// `accounts` field, which is the only place in any response that tells a reader
// an id resolves in currencyScope.
type suppressedCyclesDoc struct {
	Components struct {
		Schemas struct {
			MoneyFlowSuppressedCycle struct {
				Properties struct {
					Accounts struct {
						Description string `yaml:"description"`
					} `yaml:"accounts"`
				} `yaml:"properties"`
			} `yaml:"MoneyFlowSuppressedCycle"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// TestSuppressedCycleAccountsQualifiesItsScopeClaim pins the qualification on
// MoneyFlowSuppressedCycle.Accounts.
//
// The field said `currencyScope` "already names every one of them with its
// display metadata", and that is not always true. A link's value currency is the
// currency of the account the amount came from (linkCurrencyColumn), not of both
// its endpoints, so under ?currency=USD a suppressed cycle can name an account
// whose own currency is not USD - one currencyScope does not list. The sibling
// claim about the graph's nodes is honest about exactly this ("a non-USD account
// can appear in a ?currency=USD graph"); this one was not, and it is the one
// place a reader is told an id resolves.
//
// Prose is not something a route-parity test or a schema comparison can police,
// so this does: it reads the claim as written, in the spec and in the two
// comments that are its source, and fails if the unconditional version is back.
// The alternative is a comment that was correct once and is checked by nobody.
func TestSuppressedCycleAccountsQualifiesItsScopeClaim(t *testing.T) {
	var doc suppressedCyclesDoc
	require.NoError(t, yaml.Unmarshal(openAPISpec, &doc))
	specClaim := strings.Join(strings.Fields(doc.Components.Schemas.MoneyFlowSuppressedCycle.
		Properties.Accounts.Description), " ")
	require.NotEmpty(t, specClaim, "the accounts description is gone; nothing left to qualify")

	// The reason the unconditional claim was wrong, in the words of the node
	// sentence that was already careful about it.
	const reason = "not of both its endpoints"
	const unconditional = "names every one of them"

	assert.NotContains(t, specClaim, unconditional,
		"the spec promises unconditionally what the value-currency exception does not deliver; qualify it")
	assert.Contains(t, specClaim, reason,
		"the spec must say why a participant may fall outside currencyScope, not just that it might")

	// The same claim in the two comments it is written from, so the spec cannot be
	// corrected while the source of the wording still asserts the strong version.
	// The assertion is on the comment text alone rather than on the whole file, so
	// a failure names the sentence and not three thousand lines of context.
	for name, claim := range map[string]string{
		"models.go Accounts":        docCommentBefore(t, "models/models.go", "type MoneyFlowSuppressedCycle struct", "Accounts lists the participants"),
		"money_flow.go suppressed":  docCommentBefore(t, "handlers/money_flow.go", "func suppressedFlowCycles(", "suppressedFlowCycles renders"),
	} {
		assert.NotContainsf(t, claim, unconditional,
			"%s still makes the unconditional claim, so the spec's qualification is only as good as its source", name)
		assert.Containsf(t, claim, reason,
			"%s must say why a participant can fall outside currencyScope, not merely that it might", name)
	}
}

// docCommentBefore returns the contiguous // comment block immediately above
// marker, which is the doc comment of the declaration that follows it. Scoping
// the assertion to a comment is what lets a test police prose: the claim is
// prose, and a whole-file substring check would either be too broad (it would
// trip on an unrelated sentence) or too narrow to read in a failure.
func docCommentBefore(t *testing.T, path, marker, firstLineContains string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	at := strings.Index(string(src), marker)
	require.Greater(t, at, 0, "%s no longer contains %q", path, marker)

	lines := strings.Split(string(src[:at]), "\n")
	// The slice ends on the newline before the marker, so the last element is
	// empty; skip the blank tail before walking back over the comment.
	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	var block []string
	for i := end; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "//") {
			break
		}
		block = append([]string{line}, block...)
	}
	require.NotEmpty(t, block, "the declaration at %s has no doc comment", path)
	assert.Contains(t, strings.Join(block, "\n"), firstLineContains,
		"the comment above %q is not the one this test reads; update the marker", path)
	return strings.Join(block, "\n")
}

// TestServeOpenAPISpec checks the embedded document is actually served.
func TestServeOpenAPISpec(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	testRouter().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "yaml")
	assert.Contains(t, w.Body.String(), "openapi:")
}
