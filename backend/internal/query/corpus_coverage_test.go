package query

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestCorpusCoversEveryUserField is the completeness check on the drift guard
// itself.
//
// The corpus is what makes the Go and TypeScript parsers provably agree. It only
// does that for the forms it contains: a field added to the table with no corpus
// case would be mirrored by hand, and a mistake in the mirror would fail
// nothing. So every user-facing field must appear in at least one case, and this
// test is what notices a new field arriving without one.
func TestCorpusCoversEveryUserField(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus struct {
		Cases []struct {
			Name        string       `json:"name"`
			ServerOnly  bool         `json:"serverOnly"`
			Surface     string       `json:"surface"`
			Terms       []Term       `json:"terms"`
			Diagnostics []Diagnostic `json:"diagnostics"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}

	covered := map[string]bool{}
	for _, tc := range corpus.Cases {
		for _, term := range tc.Terms {
			covered[term.Field] = true
		}
		// A field can also be covered by a case that exercises the bad-value
		// path, which produces no term; count the surface instead.
		if colon := strings.IndexByte(tc.Surface, ':'); colon > 0 {
			candidate := strings.ToLower(strings.TrimRight(tc.Surface[:colon], "><=!~"))
			if _, ok := userField(candidate); ok {
				covered[candidate] = true
			}
		}
	}

	var missing []string
	for name, def := range fieldTable {
		if !def.userTyped {
			continue
		}
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these fields have no case in testdata/corpus.json, so the Go and TypeScript parsers are not pinned on them: %v", missing)
	}
}

// TestServerOnlyCasesAreOnlyAboutUnresolvedNames keeps the escape hatch honest.
// It exists for the two behaviours that are genuinely the server's alone: it
// resolves no names, so it must refuse one. Widening it would quietly reduce the
// corpus to a one-way guard.
func TestServerOnlyCasesAreOnlyAboutUnresolvedNames(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus struct {
		Cases []struct {
			Name       string `json:"name"`
			ServerOnly bool   `json:"serverOnly"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}

	allowed := map[string]bool{
		"a quoted name on an id field is rejected":    true,
		"a name the server cannot resolve is dropped": true,
	}
	var bad []string
	shared := 0
	for _, tc := range corpus.Cases {
		if !tc.ServerOnly {
			shared++
			continue
		}
		if !allowed[tc.Name] {
			bad = append(bad, tc.Name)
		}
	}
	if len(bad) > 0 {
		t.Errorf("serverOnly is meant only for the two cases where the server alone refuses a name; these others use it: %v", bad)
	}
	if shared == 0 {
		t.Error("every case is serverOnly, so the corpus no longer guards both parsers")
	}
}
