// Package testutil provides small helpers shared by multiple test files.
// It is intentionally simple and avoids dependencies on production packages so
// it can be imported from any test without cycle concerns.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// WriteFile creates path (and its parent directories) and writes content to it.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

// WriteFiles creates many files under root. Keys are slash-separated paths
// relative to root.
func WriteFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		WriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
}

// InitGitRepo initializes a git repository in dir with a single commit on main.
func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	RunGit(t, dir, "init", "--quiet")
	RunGit(t, dir, "config", "user.email", "test@example.com")
	RunGit(t, dir, "config", "user.name", "Test")
	RunGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte{}, 0o644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	RunGit(t, dir, "add", "-A")
	RunGit(t, dir, "commit", "-m", "fixture", "--quiet")
	RunGit(t, dir, "branch", "-m", "main")
}

// RunGit runs a git command in dir, failing the test on error.
func RunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
