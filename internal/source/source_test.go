package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/config"
	"github.com/s3-odara/gentoo-overlay/internal/testutil"
)

func TestResolve_GuruPriorityWins(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.1.ebuild": "EAPI=8\n",
	})

	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master":      guru,
		"gentoo-zh/master": zh,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

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
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.1.ebuild": "EAPI=8\n",
	})

	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master":      guru,
		"gentoo-zh/master": zh,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

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
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{})

	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master":      guru,
		"gentoo-zh/master": zh,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err == nil {
		t.Fatal("expected error for missing package")
	}
	var skip *SkippedError
	if !errors.As(err, &skip) {
		t.Fatalf("expected *SkippedError, got %T", err)
	}
}

func TestResolve_DifferentCategorySameNameNotSelected(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"app-misc/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})

	cfg := &config.Config{
		Sources:      []config.Source{{Name: "guru", URL: guru, Ref: "master"}},
		BranchPrefix: "update",
	}
	cloner := &fakeCloner{fixtures: map[string]string{"guru/master": guru}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err == nil {
		t.Fatal("expected error when same package name exists only under a different category")
	}
	var skip *SkippedError
	if !errors.As(err, &skip) {
		t.Fatalf("expected *SkippedError, got %T", err)
	}
}

func TestResolve_ExcludedPackage(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"virtual/notification-daemon/notification-daemon-1.0.ebuild": "EAPI=8\n",
	})

	cfg := &config.Config{
		Sources:      []config.Source{{Name: "guru", URL: guru, Ref: "master"}},
		Exclusions:   []string{"virtual/notification-daemon"},
		BranchPrefix: "update",
	}
	cloner := &fakeCloner{fixtures: map[string]string{"guru/master": guru}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	_, err := mgr.Resolve(context.Background(), "virtual/notification-daemon")
	if err == nil {
		t.Fatal("expected error for excluded package")
	}
	var skip *SkippedError
	if !errors.As(err, &skip) {
		t.Fatalf("expected *SkippedError, got %T", err)
	}
	if skip.Reason != "excluded" {
		t.Fatalf("expected reason excluded, got %q", skip.Reason)
	}
}

func TestResolve_SourceOverride(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.2.ebuild": "EAPI=8\n",
	})

	override := "gentoo-zh"
	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
		Overrides:    map[string]config.Override{"gui-apps/fuzzel": {Source: &override}},
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master":      guru,
		"gentoo-zh/master": zh,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	src, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if src.Name != "gentoo-zh" {
		t.Fatalf("expected gentoo-zh override, got %q", src.Name)
	}
	assertFileExists(t, filepath.Join(src.Dir, "fuzzel-1.2.ebuild"))
}

func TestResolve_RefOverride(t *testing.T) {
	fixtures := t.TempDir()
	guruMaster := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	guruStable := makeSourceFixture(t, fixtures, "guru", "stable", map[string]string{
		"gui-apps/fuzzel/fuzzel-0.9.ebuild": "EAPI=8\n",
	})

	ref := "stable"
	cfg := &config.Config{
		Sources:      []config.Source{{Name: "guru", URL: guruMaster, Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]config.Override{"gui-apps/fuzzel": {Ref: &ref}},
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master": guruMaster,
		"guru/stable": guruStable,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	src, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if src.Ref != "stable" {
		t.Fatalf("expected ref stable, got %q", src.Ref)
	}
	assertFileExists(t, filepath.Join(src.Dir, "fuzzel-0.9.ebuild"))
}

func TestResolve_HigherPrioritySourcePreparationFailureDoesNotFallback(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.1.ebuild": "EAPI=8\n",
	})

	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
	}

	cloner := &fakeClonerWithFailures{
		fakeCloner: fakeCloner{fixtures: map[string]string{
			"guru/master":      guru,
			"gentoo-zh/master": zh,
		}},
		fail: map[string]error{"guru/master": errors.New("guru clone failed")},
	}
	mgr := NewManager(cfg, cloner, t.TempDir())

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err == nil {
		t.Fatal("expected error when higher-priority source preparation fails")
	}
	var skip *SkippedError
	if errors.As(err, &skip) {
		t.Fatalf("expected real error, got *SkippedError: %v", err)
	}
	if !strings.Contains(err.Error(), "guru clone failed") {
		t.Fatalf("expected error to mention guru clone failure, got %v", err)
	}
}

func TestResolve_SourceOverrideMissing(t *testing.T) {
	fixtures := t.TempDir()
	guru := makeSourceFixture(t, fixtures, "guru", "master", map[string]string{
		"gui-apps/fuzzel/fuzzel-1.0.ebuild": "EAPI=8\n",
	})
	zh := makeSourceFixture(t, fixtures, "gentoo-zh", "master", map[string]string{})

	override := "gentoo-zh"
	cfg := &config.Config{
		Sources: []config.Source{
			{Name: "guru", URL: guru, Ref: "master"},
			{Name: "gentoo-zh", URL: zh, Ref: "master"},
		},
		BranchPrefix: "update",
		Overrides:    map[string]config.Override{"gui-apps/fuzzel": {Source: &override}},
	}

	cloner := &fakeCloner{fixtures: map[string]string{
		"guru/master":      guru,
		"gentoo-zh/master": zh,
	}}
	mgr := NewManager(cfg, cloner, t.TempDir())

	_, err := mgr.Resolve(context.Background(), "gui-apps/fuzzel")
	if err == nil {
		t.Fatal("expected error when overridden source lacks package")
	}
	var skip *SkippedError
	if !errors.As(err, &skip) {
		t.Fatalf("expected *SkippedError, got %T", err)
	}
}

type fakeCloner struct {
	fixtures map[string]string // "name/ref" -> fixture directory
}

func (f *fakeCloner) Clone(ctx context.Context, source config.Source, dst string) error {
	key := source.Name + "/" + source.Ref
	src, ok := f.fixtures[key]
	if !ok {
		return fmt.Errorf("no fixture for %s", key)
	}
	return testutil.CopyDir(src, dst)
}

// fakeClonerWithFailures wraps fakeCloner and returns injected errors for
// specific source/ref combinations. It is used to test that source preparation
// failures are propagated rather than treated as package absence.
type fakeClonerWithFailures struct {
	fakeCloner
	fail map[string]error // "name/ref" -> error to return
}

func (f *fakeClonerWithFailures) Clone(ctx context.Context, source config.Source, dst string) error {
	key := source.Name + "/" + source.Ref
	if err, ok := f.fail[key]; ok {
		return err
	}
	return f.fakeCloner.Clone(ctx, source, dst)
}

func makeSourceFixture(t *testing.T, root, name, ref string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name, ref)
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
