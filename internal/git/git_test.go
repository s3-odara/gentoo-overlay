package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README")
	runGit(t, dir, "commit", "-m", "initial", "--quiet")
	runGit(t, dir, "branch", "-m", "main")
	return dir
}

func TestExecDriver_CreateBranch(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("branch check failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "update/gui-apps-fuzzel" {
		t.Fatalf("unexpected branch: %s", out)
	}
}

func TestExecDriver_CreateBranchFromBase(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}

	// Create a commit on main so the branch base matters.
	if err := os.WriteFile(filepath.Join(dir, "base"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "base")
	runGit(t, dir, "commit", "-m", "add base", "--quiet")

	// Switch to a feature branch and add a file.
	if err := d.CreateBranch(dir, "feature", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "feature"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "feature")
	runGit(t, dir, "commit", "-m", "feature", "--quiet")

	// A new update branch created from main must not contain the feature commit.
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatalf("CreateBranch from base failed: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "log", "--pretty=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("log failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "feature") {
		t.Fatalf("update branch should not include feature commit, log:\n%s", out)
	}
}

func TestExecDriver_StageAndCommit(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatal(err)
	}
	pkgFile := filepath.Join(dir, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild")
	if err := os.MkdirAll(filepath.Dir(pkgFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkgFile, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Stage(dir, "gui-apps/fuzzel"); err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	changed, err := d.HasStagedChanges(dir, "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("HasStagedChanges failed: %v", err)
	}
	if !changed {
		t.Fatal("expected staged changes")
	}
	if err := d.Commit(dir, "sync gui-apps/fuzzel"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("log failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "sync gui-apps/fuzzel" {
		t.Fatalf("unexpected commit message: %s", out)
	}
}

func TestExecDriver_PushErrorMentionsContentsWrite(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatal(err)
	}
	err := d.Push(dir, "origin", "update/gui-apps-fuzzel")
	if err == nil {
		t.Fatal("expected push to fail without remote")
	}
	if !strings.Contains(err.Error(), "contents: write") {
		t.Fatalf("error should mention contents: write, got: %v", err)
	}
}

func TestExecDriver_Checkout(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "feature", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	if err := d.Checkout(dir, "main"); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("branch check failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("expected main, got: %s", out)
	}
}

func TestExecDriver_ResetHard(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.ResetHard(dir); err != nil {
		t.Fatalf("ResetHard failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale")); !os.IsNotExist(err) {
		t.Fatalf("expected unstaged file to be removed after hard reset, got err=%v", err)
	}
}

func TestExecDriver_Stage_PathScoped(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	if err := d.CreateBranch(dir, "update/gui-apps-fuzzel", "main"); err != nil {
		t.Fatal(err)
	}

	// Write one file inside the package path and one outside.
	pkgFile := filepath.Join(dir, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild")
	otherFile := filepath.Join(dir, "README")
	if err := os.MkdirAll(filepath.Dir(pkgFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkgFile, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := d.Stage(dir, "gui-apps/fuzzel"); err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	if err := d.Commit(dir, "sync gui-apps/fuzzel"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// The commit should only contain the package file.
	out, err := exec.Command("git", "-C", dir, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("diff-tree failed: %v\n%s", err, out)
	}
	names := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(names) != 1 || names[0] != "gui-apps/fuzzel/fuzzel-1.0.ebuild" {
		t.Fatalf("expected only gui-apps/fuzzel/fuzzel-1.0.ebuild, got %v", names)
	}
}

func TestExecDriver_ResolveHead(t *testing.T) {
	dir := initRepo(t)
	d := &ExecDriver{}
	sha, err := d.ResolveHead(dir)
	if err != nil {
		t.Fatalf("ResolveHead failed: %v", err)
	}
	if len(sha) != 12 {
		t.Fatalf("expected 12-character short SHA, got %q", sha)
	}
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("expected hex SHA, got %q", sha)
		}
	}
	full, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD failed: %v\n%s", err, full)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(full)), sha) {
		t.Fatalf("short SHA %q is not a prefix of HEAD %q", sha, strings.TrimSpace(string(full)))
	}
}
