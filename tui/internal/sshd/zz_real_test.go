package sshd

import (
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Drives the door against the REAL local API to isolate data as the variable.
func TestZZRealAPI(t *testing.T) {
	cfg := testConfig(t, "http://localhost:8080/api/v1")
	addr := startServer(t, cfg)

	client := dial(t, addr, "tui@fintrak.local", "tui-smoke-test-123")
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()
	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{}); err != nil {
		t.Fatalf("pty: %v", err)
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}

	// Capture everything, not just up to a marker.
	type res struct{ b strings.Builder }
	ch := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 1<<16)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
				ch <- b.String()
			}
			if err != nil {
				return
			}
		}
	}()

	deadline := time.After(12 * time.Second)
	var last string
	for {
		select {
		case f := <-ch:
			last = f
			if strings.Contains(f, "Dashboard") {
				t.Logf("workspace rendered (%d bytes)", len(f))
				return
			}
		case <-deadline:
			t.Fatalf("no workspace; %d bytes: %q", len(last), last)
		}
	}
}
