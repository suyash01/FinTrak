package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

// writeModule lays out a throwaway module so the source-derived exclusion can be
// exercised: main.go holds func main(), lib.go holds ordinary code.
func writeModule(t *testing.T, module, mainBody string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module " + module + "\n\ngo 1.27\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n" +
			mainBody +
			"}\n\nfunc helper() int {\n\treturn 1\n}\n\nfunc unused() int {\n\tfmt.Println(helper())\n\treturn 2\n}\n",
		"lib.go": "package main\n\nfunc lib() int {\n\treturn 3\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestSummarizeSkipsTheExcludedRange(t *testing.T) {
	path := writeProfile(t, `mode: atomic
github.com/fintrak/backend/main.go:50.2,51.1 10 0
github.com/fintrak/backend/main.go:120.2,121.1 5 3
github.com/fintrak/backend/handlers/account.go:30.1,32.1 4 0
github.com/fintrak/backend/handlers/account.go:40.1,42.1 6 6
`)
	exclusions := map[string]lineRange{
		"github.com/fintrak/backend/main.go": {start: 49, end: 98},
	}

	total, covered, err := summarize(path, exclusions)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// Only the block inside main() is dropped: 10 statements that no unit test
	// can reach.
	if total != 15 || covered != 11 {
		t.Fatalf("got total=%d covered=%d, want 15/11", total, covered)
	}
}

func TestSummarizeMissingFileErrors(t *testing.T) {
	if _, _, err := summarize(filepath.Join(t.TempDir(), "absent.out"), nil); err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestIsExcluded(t *testing.T) {
	exclusions := map[string]lineRange{
		"github.com/fintrak/backend/main.go":       {start: 49, end: 98},
		"github.com/fintrak/backend/other/main.go": {start: 10, end: 20},
	}
	tests := []struct {
		loc  string
		want bool
	}{
		{"github.com/fintrak/backend/main.go:49.1,51.1", true},
		{"github.com/fintrak/backend/main.go:98.2,98.9", true},
		{"github.com/fintrak/backend/main.go:120.2,121.1", false},
		{"github.com/fintrak/backend/handlers/account.go:50.1,51.1", false},
		// A different main.go is not excluded just because it shares the name.
		{"github.com/fintrak/backend/handlers/main.go:50.1,51.1", false},
		{"github.com/fintrak/backend/other/main.go:15.1,16.1", true},
		{"github.com/fintrak/backend/main.go:not-a-line", false},
		{"no-colon-at-all", false},
	}
	for _, tt := range tests {
		if got := isExcluded(tt.loc, exclusions); got != tt.want {
			t.Errorf("isExcluded(%q) = %v, want %v", tt.loc, got, tt.want)
		}
	}
}

// The exclusion is what the source says it is: func main()'s own lines, derived
// per file, so a library in the same module keeps being measured in full and a
// command's helper functions are no longer exempt.
func TestMainExclusionsComeFromTheSource(t *testing.T) {
	root := writeModule(t, "example.com/demo", "\tprintln(\"boot\")\n")

	files := []string{
		"example.com/demo/main.go",
		"example.com/demo/lib.go",
		"example.com/other/main.go", // outside the module: nothing to resolve
	}
	exclusions, err := mainExclusions(files, root)
	if err != nil {
		t.Fatalf("mainExclusions: %v", err)
	}

	got, ok := exclusions["example.com/demo/main.go"]
	if !ok {
		t.Fatal("main.go was not excluded")
	}
	// func main() is the fifth line and closes on the seventh in the fixture.
	if got.start != 5 || got.end != 7 {
		t.Errorf("excluded lines %d-%d, want 5-7", got.start, got.end)
	}
	if _, ok := exclusions["example.com/demo/lib.go"]; ok {
		t.Error("a file without func main() must not be excluded")
	}
	if _, ok := exclusions["example.com/other/main.go"]; ok {
		t.Error("a file outside the module must not be excluded")
	}
}

func TestMainFuncRangeIgnoresMethods(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	body := "package main\n\ntype t struct{}\n\nfunc (t) main() {}\n\nfunc other() {}\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, ok := mainFuncRange(src); ok {
		t.Error("a method named main must not be treated as the entry point")
	}
}

func TestModulePathReadsGoMod(t *testing.T) {
	root := writeModule(t, "example.com/demo", "")
	module, err := modulePath(root)
	if err != nil {
		t.Fatalf("modulePath: %v", err)
	}
	if module != "example.com/demo" {
		t.Errorf("module = %q", module)
	}

	if _, err := modulePath(t.TempDir()); err == nil {
		t.Error("expected an error when go.mod is missing")
	}
}

func TestDescribeExclusions(t *testing.T) {
	if got := describeExclusions(nil); got != "nothing" {
		t.Errorf("empty exclusions = %q", got)
	}
	got := describeExclusions(map[string]lineRange{
		"example.com/demo/cmd/a/main.go": {start: 3, end: 9},
		"example.com/demo/main.go":       {start: 5, end: 7},
	})
	if !strings.Contains(got, "main.go:5-7") || !strings.Contains(got, "main.go:3-9") {
		t.Errorf("describeExclusions = %q", got)
	}
}
