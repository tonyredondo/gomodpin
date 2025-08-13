package main

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Test that processMapOrdered iterates keys in lexicographic order.
func TestProcessMapOrdered_SortsLexicographically(t *testing.T) {
	modules := map[string]string{
		"github.com/z/project": "v1.2.3",
		"github.com/a/project": "v0.1.0",
		"golang.org/x/sys":     "v0.16.0",
	}

	var visited []string
	processMapOrdered(modules, func(key, value string) {
		visited = append(visited, key)
	})

	expected := make([]string, 0, len(modules))
	for k := range modules {
		expected = append(expected, k)
	}
	sort.Strings(expected)

	if len(visited) != len(expected) {
		t.Fatalf("unexpected visited length: got %d want %d", len(visited), len(expected))
	}
	for i := range expected {
		if visited[i] != expected[i] {
			t.Fatalf("order mismatch at %d: got %q want %q", i, visited[i], expected[i])
		}
	}
}

func runMainWithArgs(t *testing.T, args []string) {
	t.Helper()

	// Save and restore global state used by main.
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Reset the default flag set so main() can register flags cleanly each run.
	origCommandLine := flag.CommandLine
	defer func() { flag.CommandLine = origCommandLine }()
	flag.CommandLine = flag.NewFlagSet("gomodpin-test", flag.ContinueOnError)

	os.Args = append([]string{"gomodpin"}, args...)

	// Execute main. Any fatal error would fail the test process; construct inputs to avoid that.
	main()
}

// Integration-style test: verifies that a replace block is appended and default excludes are respected.
func TestMain_AppendsReplaceBlock_RespectsDefaultExcludes(t *testing.T) {
	tempDir := t.TempDir()
	modPath := filepath.Join(tempDir, "go.mod")

	original := strings.TrimSpace(`
module example.com/test

go 1.23.2

require (
	github.com/pkg/errors v0.9.1
	gopkg.in/DataDog/dd-trace-go.v1 v1.59.0
)
`)
	if err := os.WriteFile(modPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	runMainWithArgs(t, []string{modPath})

	// Backup must exist and match original content.
	backupPath := filepath.Join(tempDir, "go.mod.old")
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if strings.TrimSpace(string(backupData)) != original {
		t.Fatalf("backup content mismatch")
	}

	// The new go.mod must contain the appended replace block without the default-excluded module.
	newData, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatalf("read modified go.mod: %v", err)
	}
	content := string(newData)

	// It should include pkg/errors replacement and exclude dd-trace-go by default.
	if !strings.Contains(content, "\n\n// prevent module upgrades\nreplace (") {
		t.Fatalf("missing replace block header")
	}
	if !strings.Contains(content, "\tgithub.com/pkg/errors => github.com/pkg/errors v0.9.1\n") {
		t.Fatalf("missing expected replacement for pkg/errors")
	}
	// Only search within the appended replace block, not the original require section.
	headerIdx := strings.Index(content, "\n\n// prevent module upgrades\nreplace (")
	tail := content
	if headerIdx >= 0 {
		tail = content[headerIdx:]
	}
	if strings.Contains(tail, "dd-trace-go") {
		t.Fatalf("default-excluded module appears in replace block")
	}
}
