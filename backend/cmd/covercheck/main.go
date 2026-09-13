// Command covercheck computes statement coverage from a Go coverage profile
// while excluding a file's process-bootstrap region (by default main() in
// main.go), which cannot be exercised by unit tests. It exits non-zero when the
// result falls below the requested minimum, so CI can enforce a floor.
//
// Usage:
//
//	go run ./cmd/covercheck -profile coverage.out -min 85
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// excludedMainStart and excludedMainEnd bound the func main() body in main.go.
// The coverage profile records source line ranges, and main() is only reachable
// by launching the real process, so it is excluded from the enforced floor.
const (
	excludedFile  = "main.go"
	excludedStart = 49
	excludedEnd   = 98
)

func main() {
	profile := flag.String("profile", "coverage.out", "coverage profile to read")
	min := flag.Float64("min", 0, "minimum statement coverage percentage (0 disables the gate)")
	flag.Parse()

	total, covered, err := summarize(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(1)
	}
	if total == 0 {
		fmt.Fprintln(os.Stderr, "covercheck: no statements found in profile")
		os.Exit(1)
	}

	pct := float64(covered) / float64(total) * 100
	fmt.Printf("coverage (excluding %s main(): lines %d-%d): %.2f%% (%d/%d statements)\n",
		excludedFile, excludedStart, excludedEnd, pct, covered, total)

	if *min > 0 && pct < *min {
		fmt.Fprintf(os.Stderr, "covercheck: coverage %.2f%% is below the %.2f%% floor\n", pct, *min)
		os.Exit(1)
	}
}

// summarize parses a Go coverage profile and returns the total and covered
// statement counts, skipping blocks that fall inside main().
func summarize(path string) (total, covered int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
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
		if isExcluded(fields[0]) {
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
// falls inside the bootstrap region.
func isExcluded(loc string) bool {
	file := loc
	if i := strings.LastIndex(loc, ":"); i >= 0 {
		file = loc[:i]
	}
	if filepath.Base(file) != excludedFile {
		return false
	}
	start := loc[strings.LastIndex(loc, ":")+1:]
	if i := strings.IndexAny(start, ".,"); i >= 0 {
		start = start[:i]
	}
	line, err := strconv.Atoi(start)
	if err != nil {
		return false
	}
	return line >= excludedStart && line <= excludedEnd
}
