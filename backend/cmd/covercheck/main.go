// Command covercheck computes statement coverage from a Go coverage profile
// while excluding each command's process-bootstrap region — the body of its
// func main(), which unit tests cannot reach because only launching the binary
// runs it. It exits non-zero when the result falls below the requested minimum,
// so CI can enforce a floor.
//
// The excluded region is derived from the source rather than hard-coded: every
// profile entry that resolves to a file with a func main() contributes its line
// range, and nothing else is skipped. -root names the directory of the module
// the profile was recorded in, because a profile records import paths
// ("github.com/fintrak/backend/main.go") and the tui and mcp floors are checked
// from the backend's directory against profiles of other modules.
//
// Usage:
//
//	go run ./cmd/covercheck -profile coverage.out -min 85
//	go run ./cmd/covercheck -profile ../tui/coverage.out -min 18 -root ../tui
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// lineRange is an inclusive span of source lines.
type lineRange struct{ start, end int }

func main() {
	profile := flag.String("profile", "coverage.out", "coverage profile to read")
	min := flag.Float64("min", 0, "minimum statement coverage percentage (0 disables the gate)")
	root := flag.String("root", ".", "directory of the module the profile was recorded in (its go.mod names the import prefix)")
	flag.Parse()

	files, err := profileFiles(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(1)
	}
	exclusions, err := mainExclusions(files, *root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(1)
	}

	total, covered, err := summarize(*profile, exclusions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(1)
	}
	if total == 0 {
		fmt.Fprintln(os.Stderr, "covercheck: no statements found in profile")
		os.Exit(1)
	}

	pct := float64(covered) / float64(total) * 100
	fmt.Printf("coverage (excluding %s): %.2f%% (%d/%d statements)\n",
		describeExclusions(exclusions), pct, covered, total)

	if *min > 0 && pct < *min {
		fmt.Fprintf(os.Stderr, "covercheck: coverage %.2f%% is below the %.2f%% floor\n", pct, *min)
		os.Exit(1)
	}
}

// describeExclusions renders what was skipped, so the printed number says what
// it measures.
func describeExclusions(exclusions map[string]lineRange) string {
	if len(exclusions) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(exclusions))
	for file, r := range exclusions {
		parts = append(parts, fmt.Sprintf("func main() in %s:%d-%d", filepath.Base(file), r.start, r.end))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// profileFiles returns the distinct source files a profile mentions, in the
// order they first appear.
func profileFiles(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := map[string]bool{}
	var files []string
	scanner := newProfileScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		file, _, ok := splitLocation(fields[0])
		if !ok || seen[file] {
			continue
		}
		seen[file] = true
		files = append(files, file)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

// mainExclusions maps every profile file that defines func main() to the lines
// that function occupies. Files outside the module named by root's go.mod, and
// files without a func main(), contribute nothing: a library package is measured
// in full.
func mainExclusions(files []string, root string) (map[string]lineRange, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}

	out := map[string]lineRange{}
	for _, file := range files {
		rel, ok := strings.CutPrefix(file, module+"/")
		if !ok {
			continue
		}
		if r, ok := mainFuncRange(filepath.Join(root, filepath.FromSlash(rel))); ok {
			out[file] = r
		}
	}
	return out, nil
}

// modulePath reads the module path from root's go.mod, which is what a profile's
// import paths are prefixed with.
func modulePath(root string) (string, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			if module := strings.TrimSpace(rest); module != "" {
				return module, nil
			}
		}
	}
	return "", fmt.Errorf("no module directive in %s", path)
}

// mainFuncRange returns the line range of func main() in src, reporting false
// when the file cannot be parsed or has no func main().
func mainFuncRange(src string) (lineRange, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		return lineRange{}, false
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "main" {
			continue
		}
		return lineRange{fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line}, true
	}
	return lineRange{}, false
}

// summarize parses a Go coverage profile and returns the total and covered
// statement counts, skipping blocks that fall inside an excluded func main().
func summarize(path string, exclusions map[string]lineRange) (total, covered int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := newProfileScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, 0, fmt.Errorf("parse statements in %q: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return 0, 0, fmt.Errorf("parse count in %q: %w", line, err)
		}
		if isExcluded(fields[0], exclusions) {
			continue
		}
		total += stmts
		if count > 0 {
			covered += stmts
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return total, covered, nil
}

// isExcluded reports whether a profile location like "pkg/main.go:50.2,51.1"
// starts inside an excluded bootstrap region.
func isExcluded(loc string, exclusions map[string]lineRange) bool {
	file, start, ok := splitLocation(loc)
	if !ok {
		return false
	}
	r, ok := exclusions[file]
	if !ok {
		return false
	}
	return start >= r.start && start <= r.end
}

// splitLocation splits a profile location ("pkg/main.go:50.2,51.1") into its
// file and its first line.
func splitLocation(loc string) (string, int, bool) {
	file, rng, ok := strings.Cut(loc, ":")
	if !ok {
		return "", 0, false
	}
	start := rng
	if i := strings.IndexAny(start, ".,"); i >= 0 {
		start = start[:i]
	}
	line, err := strconv.Atoi(start)
	if err != nil {
		return "", 0, false
	}
	return file, line, true
}

// newProfileScanner reads a profile with room for long lines.
func newProfileScanner(f *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return scanner
}
