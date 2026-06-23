package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPackages_FindsAndSortsPackages(t *testing.T) {
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

	want := []string{"app-misc/brightnessctl", "app-misc/lf", "gui-apps/fuzzel"}
	for i, id := range want {
		if pkgs[i].ID != id {
			t.Fatalf("package %d: want %q, got %q", i, id, pkgs[i].ID)
		}
	}
}

func TestDiscoverPackages_SkipsRepoDirsAndEmptyPackages(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, "gui-apps", "fuzzel", "fuzzel-1.0.ebuild")

	// Repository plumbing directories must not be treated as categories.
	for _, d := range []string{".git", ".agents", ".github", "metadata", "profiles"} {
		writeFile(t, filepath.Join(root, d, "marker"), "")
	}
	// A category directory without ebuilds must not be reported as a package.
	writeDir(t, filepath.Join(root, "gui-apps", "no-ebuild"))
	writeFile(t, filepath.Join(root, "gui-apps", "no-ebuild", "README"), "")

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

func writePkg(t *testing.T, root, category, pkg, ebuild string) {
	t.Helper()
	dir := filepath.Join(root, category, pkg)
	writeDir(t, dir)
	writeFile(t, filepath.Join(dir, ebuild), "EAPI=8\n")
}

func writeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
