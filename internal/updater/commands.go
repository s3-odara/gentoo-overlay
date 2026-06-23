package updater

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecCommandRunner shells out to Gentoo tooling. It is kept small and free of
// external Go dependencies so the updater can run wherever pkgdev and pkgcheck
// are available.
type ExecCommandRunner struct{}

// RunManifest regenerates the Manifest for the package at pkgPath (relative to
// repoDir) by running pkgdev manifest inside the package directory.
func (e *ExecCommandRunner) RunManifest(repoDir, pkgPath string) error {
	pkgDir := filepath.Join(repoDir, pkgPath)
	cmd := exec.Command("pkgdev", "manifest")
	cmd.Dir = pkgDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pkgdev manifest in %s: %w\n%s", pkgPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunPkgcheck runs pkgcheck scan for the package at pkgPath (relative to
// repoDir) from the repository root.
func (e *ExecCommandRunner) RunPkgcheck(repoDir, pkgPath string) error {
	cmd := exec.Command("pkgcheck", "scan", pkgPath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pkgcheck scan %s: %w\n%s", pkgPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
