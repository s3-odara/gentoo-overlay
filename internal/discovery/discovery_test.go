package discovery

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/testutil"
)

func TestDiscoverPackages_FindsPackages(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild")
	writePkg(t, root, "app-misc", "lf", "lf-41.ebuild")
	writePkg(t, root, "app-misc", "brightnessctl", "brightnessctl-1.0.ebuild")

	pkgs, err := DiscoverPackages(root)
	if err != nil {
		t.Fatalf("DiscoverPackages failed: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(pkgs))
	}

	got := make([]string, len(pkgs))
	for i, p := range pkgs {
		got[i] = p.ID
	}
	sort.Strings(got)
	want := []string{"app-misc/brightnessctl", "app-misc/lf", "gui-apps/fuzzel"}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("package %d: want %q, got %q", i, id, got[i])
		}
	}
}

func TestDiscoverPackages_SkipsRepoDirsAndEmptyPackages(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild")

	// Repository plumbing directories must not be treated as categories.
	for _, d := range []string{".git", ".agents", ".github", "metadata", "profiles"} {
		testutil.WriteFile(t, filepath.Join(root, d, "marker"), "")
	}
	// A category directory without ebuilds must not be reported as a package.
	if err := os.MkdirAll(filepath.Join(root, "gui-apps", "no-ebuild"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, filepath.Join(root, "gui-apps", "no-ebuild", "README"), "")

	pkgs, err := DiscoverPackages(root)
	if err != nil {
		t.Fatalf("DiscoverPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].ID != "gui-apps/fuzzel" {
		t.Fatalf("expected only gui-apps/fuzzel, got %v", pkgs)
	}
}

func TestDiscoverPackages_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	pkgs, err := DiscoverPackages(root)
	if err != nil {
		t.Fatalf("DiscoverPackages failed: %v", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("expected no packages, got %d", len(pkgs))
	}
}

func writePkg(t *testing.T, root, category, pkgName, ebuild string) {
	t.Helper()
	dir := filepath.Join(root, category, pkgName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	testutil.WriteFile(t, filepath.Join(dir, ebuild), "EAPI=8\n")
}
