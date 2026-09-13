package main

import (
	"os"
	"path/filepath"
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

func TestSummarizeSkipsBootstrapAndCountsCovered(t *testing.T) {
	path := writeProfile(t, `mode: atomic
github.com/fintrak/backend/main.go:50.2,51.1 10 0
github.com/fintrak/backend/main.go:120.2,121.1 5 3
github.com/fintrak/backend/handlers/account.go:30.1,32.1 4 0
github.com/fintrak/backend/handlers/account.go:40.1,42.1 6 6
`)

	total, covered, err := summarize(path)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if total != 15 || covered != 11 {
		t.Fatalf("got total=%d covered=%d, want 15/11", total, covered)
	}
}

func TestSummarizeMissingFileErrors(t *testing.T) {
	if _, _, err := summarize(filepath.Join(t.TempDir(), "absent.out")); err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		loc  string
		want bool
	}{
		{"github.com/fintrak/backend/main.go:49.1,51.1", true},
		{"github.com/fintrak/backend/main.go:98.2,98.9", true},
		{"github.com/fintrak/backend/main.go:120.2,121.1", false},
		{"github.com/fintrak/backend/handlers/account.go:50.1,51.1", false},
		{"github.com/fintrak/backend/main.go:not-a-line", false},
	}
	for _, tt := range tests {
		if got := isExcluded(tt.loc); got != tt.want {
			t.Errorf("isExcluded(%q) = %v, want %v", tt.loc, got, tt.want)
		}
	}
}
