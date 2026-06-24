package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/overlay"
	"github.com/s3-odara/gentoo-overlay/internal/testutil"
)

func TestResolve_GuruPriorityWins(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.1.ebuild": "EAPI=8\n",
	})

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru":      guru,
		"gentoo-zh": zh,
	}}
	mgr := NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	src, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if src.Name != "guru" {
		t.Fatalf("expected guru, got %q", src.Name)
	}
	if src.Ref != "master" {
		t.Fatalf("expected ref master, got %q", src.Ref)
	}
	if src.SHA == "" {
		t.Fatal("expected non-empty SHA")
	}
	assertFileExists(t, filepath.Join(src.Dir, "fuzzel-1.0.ebuild"))
}

func TestResolve_FallbackToGentooZh(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.1.ebuild": "EAPI=8\n",
	})

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru":      guru,
		"gentoo-zh": zh,
	}}
	mgr := NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	src, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if src.Name != "gentoo-zh" {
		t.Fatalf("expected gentoo-zh, got %q", src.Name)
	}
}

func TestResolve_MissingFromAllSources(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{})

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru":      guru,
		"gentoo-zh": zh,
	}}
	mgr := NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %T: %v", err, err)
	}
}

func TestResolve_DifferentCategorySameNameNotSelected(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{
		"app-misc/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{})

	cloner := &fakeCloner{fixtures: map[string]string{"guru": guru, "gentoo-zh": zh}}
	mgr := NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %T: %v", err, err)
	}
}

func TestResolve_ExcludedPackage(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", map[string]string{
		"virtual/notification-daemon/notification-daemon-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", map[string]string{})

	cloner := &fakeCloner{fixtures: map[string]string{"guru": guru, "gentoo-zh": zh}}
	mgr := NewManager(cloner, t.TempDir())
	if err := mgr.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	_, err := mgr.Resolve(context.Background(), "virtual/notification-daemon")
	if !errors.Is(err, ErrExcluded) {
		t.Fatalf("expected ErrExcluded, got %T: %v", err, err)
	}
}

type fakeCloner struct {
	fixtures map[string]string // source name -> fixture directory
}

func (f *fakeCloner) Clone(ctx context.Context, source Source, dst string) error {
	src, ok := f.fixtures[source.Name]
	if !ok {
		return fmt.Errorf("no fixture for %s", source.Name)
	}
	return overlay.SyncRepo(src, dst)
}

func makeSourceFixture(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	// Every fixture must have at least one tracked file so git can create a
	// commit and resolve a HEAD SHA.
	testutil.WriteFile(t, filepath.Join(dir, ".gitkeep"), "")
	for path, content := range files {
		testutil.WriteFile(t, filepath.Join(dir, path), content)
	}
	testutil.InitGitRepo(t, dir)
	return dir
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}
