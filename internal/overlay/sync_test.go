package overlay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/source"
)

func TestSyncRepo_CopiesPackageContents(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "Manifest"), "DIST foo.tar.gz 123 BLAKE2B abc SHA512 def\n")
	writeFile(t, filepath.Join(src, "fuzzel-1.0.ebuild"), "EAPI=8\n")
	writeFile(t, filepath.Join(src, "files", "fix.patch"), "diff\n")

	dst := t.TempDir()
	dstDir := filepath.Join(dst, "fuzzel")
	if err := SyncRepo(src, dstDir); err != nil {
		t.Fatalf("SyncRepo failed: %v", err)
	}

	assertExists(t, filepath.Join(dstDir, "Manifest"))
	assertExists(t, filepath.Join(dstDir, "fuzzel-1.0.ebuild"))
	assertExists(t, filepath.Join(dstDir, "files", "fix.patch"))
}

func TestSyncRepo_MultiplePackages(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"fuzzel", "fnott"} {
		src := filepath.Join(root, name+"-src")
		writeFile(t, filepath.Join(src, name+"-1.0.ebuild"), "EAPI=8\n")
		dst := filepath.Join(root, "gui-apps", name)
		if err := SyncRepo(src, dst); err != nil {
			t.Fatalf("SyncRepo %s failed: %v", name, err)
		}
	}

	assertExists(t, filepath.Join(root, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild"))
	assertExists(t, filepath.Join(root, "gui-apps", "fnott", "fnott-1.0.ebuild"))
}

func TestSyncRepo_PreservesSymlink(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "fuzzel-1.0.ebuild"), "EAPI=8\n")
	if err := os.Symlink("fuzzel-1.0.ebuild", filepath.Join(src, "fuzzel.ebuild")); err != nil {
		t.Fatalf("create relative symlink: %v", err)
	}

	dst := t.TempDir()
	dstDir := filepath.Join(dst, "fuzzel")
	if err := SyncRepo(src, dstDir); err != nil {
		t.Fatalf("SyncRepo failed: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dstDir, "fuzzel.ebuild"))
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected fuzzel.ebuild to remain a symlink, mode=%s", info.Mode())
	}
	target, err := os.Readlink(filepath.Join(dstDir, "fuzzel.ebuild"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "fuzzel-1.0.ebuild" {
		t.Fatalf("unexpected symlink target: %q", target)
	}
}

func TestSyncRepo_ReplacesExistingDestination(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "fuzzel-src")
	dstDir := filepath.Join(root, "gui-apps", "fuzzel")

	writeFile(t, filepath.Join(dstDir, "fuzzel-1.0.ebuild"), "old ebuild\n")
	writeFile(t, filepath.Join(dstDir, "removed.patch"), "old patch\n")
	writeFile(t, filepath.Join(src, "fuzzel-1.0.ebuild"), "new ebuild\n")

	if err := SyncRepo(src, dstDir); err != nil {
		t.Fatalf("SyncRepo failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "fuzzel-1.0.ebuild"))
	if err != nil {
		t.Fatalf("read synced ebuild: %v", err)
	}
	if string(got) != "new ebuild\n" {
		t.Fatalf("destination was not replaced: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "removed.patch")); !os.IsNotExist(err) {
		t.Fatalf("stale destination file should be removed, got err=%v", err)
	}
}

// TestSyncRepo_FromSourceManagerDir is a regression test for the production
// contract between source.Manager.Resolve (which returns a package directory)
// and overlay.SyncRepo. It ensures a real resolved source directory can be
// synchronized into a local package directory without requiring a .git
// directory inside the package path.
func TestSyncRepo_FromSourceManagerDir(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
		"gui-apps/fuzzel/files/fix.patch":   "diff\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{})

	cloner := &fixtureCloner{fixtures: map[string]string{"guru": guru, "gentoo-zh": zh}}
	mgr := source.NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	src, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	dst := t.TempDir()
	dstDir := filepath.Join(dst, "fuzzel")
	if err := SyncRepo(src.Dir, dstDir); err != nil {
		t.Fatalf("SyncRepo failed: %v", err)
	}

	assertExists(t, filepath.Join(dstDir, "fuzzel-1.0.ebuild"))
	assertExists(t, filepath.Join(dstDir, "files", "fix.patch"))
}

type fixtureCloner struct {
	fixtures map[string]string // source name -> fixture directory
}

func (f *fixtureCloner) Clone(_ context.Context, s source.Source, dst string) error {
	src, ok := f.fixtures[s.Name]
	if !ok {
		return fmt.Errorf("no fixture for %s", s.Name)
	}
	return copyDir(src, dst)
}

func makeSourceFixture(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, ".gitkeep"), "")
	for path, content := range files {
		writeFile(t, filepath.Join(dir, path), content)
	}
	initGitRepo(t, dir)
	return dir
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "--quiet")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "fixture", "--quiet")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}
