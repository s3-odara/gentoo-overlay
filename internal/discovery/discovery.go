package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// repoDirs are top-level directories that belong to the repository plumbing
// rather than Gentoo package categories.
var repoDirs = map[string]bool{
	".git":     true,
	".agents":  true,
	".github":  true,
	"metadata": true,
	"profiles": true,
}

// Package describes a local Gentoo package discovered under the overlay root.
type Package struct {
	ID       string // category/package
	Category string
	Name     string
	Path     string // absolute path to the package directory
}

// DiscoverPackages scans rootDir for Gentoo packages laid out as
// <category>/<package>. A directory is treated as a package candidate only when
// it directly contains at least one *.ebuild file. Non-package repository
// directories are ignored and results are returned in deterministic
// lexicographic order by package ID.
func DiscoverPackages(rootDir string) ([]Package, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("read overlay root %q: %w", rootDir, err)
	}

	var pkgs []Package
	for _, catEntry := range entries {
		if !catEntry.IsDir() {
			continue
		}
		category := catEntry.Name()
		if repoDirs[category] {
			continue
		}

		catPath := filepath.Join(rootDir, category)
		pkgEntries, err := os.ReadDir(catPath)
		if err != nil {
			return nil, fmt.Errorf("read category %q: %w", category, err)
		}
		for _, pkgEntry := range pkgEntries {
			if !pkgEntry.IsDir() {
				continue
			}
			pkgName := pkgEntry.Name()
			pkgPath := filepath.Join(catPath, pkgName)
			if !hasEbuild(pkgPath) {
				continue
			}
			pkgs = append(pkgs, Package{
				ID:       category + "/" + pkgName,
				Category: category,
				Name:     pkgName,
				Path:     pkgPath,
			})
		}
	}

	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	return pkgs, nil
}

func hasEbuild(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".ebuild" {
			return true
		}
	}
	return false
}
