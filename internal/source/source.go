package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/s3-odara/gentoo-overlay/internal/config"
)

// Cloner obtains a local clone of a source overlay at the requested ref.
// Production code shells out to git; tests can provide fixture cloners that
// copy local repositories.
type Cloner interface {
	Clone(ctx context.Context, source config.Source, dst string) error
}

// GitCloner clones source overlays with git using a shallow clone at the
// requested ref.
type GitCloner struct{}

// Clone performs a shallow clone of source.URL at source.Ref into dst.
func (g *GitCloner) Clone(ctx context.Context, source config.Source, dst string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1", "--branch", source.Ref, source.URL, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone %q at %q: %w\n%s", source.Name, source.Ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResolvedSource describes the source overlay selected for a package.
type ResolvedSource struct {
	Name string
	URL  string
	Dir  string // package directory in the source overlay at the effective ref
	Ref  string // effective ref (default or override)
	SHA  string // resolved commit SHA at the effective ref
}

// SkippedError reports that a package is skipped because it is excluded or
// missing from the configured source overlays.
type SkippedError struct {
	Package string
	Reason  string
}

func (e *SkippedError) Error() string {
	return fmt.Sprintf("package %q skipped: %s", e.Package, e.Reason)
}

// Manager initializes and queries source overlays for package lookup.
type Manager struct {
	cfg    *config.Config
	cloner Cloner
	base   string
	// dirs caches cloned source directories keyed by source name and ref.
	dirs map[string]map[string]string
}

// NewManager returns a manager that clones sources under baseDir.
func NewManager(cfg *config.Config, cloner Cloner, baseDir string) *Manager {
	return &Manager{
		cfg:    cfg,
		cloner: cloner,
		base:   baseDir,
		dirs:   make(map[string]map[string]string),
	}
}

// Initialize eagerly clones every configured source at its default ref so that
// subsequent resolution does not hit the network.
func (m *Manager) Initialize(ctx context.Context) error {
	for _, s := range m.cfg.Sources {
		if _, err := m.dirFor(ctx, s, s.Ref); err != nil {
			return fmt.Errorf("initialize source %q: %w", s.Name, err)
		}
	}
	return nil
}

// Resolve selects the source overlay and ref for pkg, verifies the package path
// exists at the effective ref, and returns the resolved commit SHA. If the
// package is excluded or not present in any configured source, a *SkippedError
// is returned so callers can distinguish skipped packages from real failures.
func (m *Manager) Resolve(ctx context.Context, pkg string) (ResolvedSource, error) {
	if m.cfg.IsExcluded(pkg) {
		return ResolvedSource{}, &SkippedError{Package: pkg, Reason: "excluded"}
	}

	category, pkgName, err := splitPackageID(pkg)
	if err != nil {
		return ResolvedSource{}, err
	}

	existsIn := func(name string) (bool, error) {
		s, ok := m.cfg.SourceByName(name)
		if !ok {
			return false, nil
		}
		dir, err := m.dirFor(ctx, s, s.Ref)
		if err != nil {
			return false, fmt.Errorf("prepare source %q at %q: %w", s.Name, s.Ref, err)
		}
		return packageExists(dir, category, pkgName), nil
	}

	source, ref, err := m.cfg.ResolveSource(pkg, existsIn)
	if err != nil {
		if errors.Is(err, config.ErrPackageExcluded) || errors.Is(err, config.ErrPackageNotFound) {
			return ResolvedSource{}, &SkippedError{Package: pkg, Reason: err.Error()}
		}
		return ResolvedSource{}, err
	}

	dir, err := m.dirFor(ctx, source, ref)
	if err != nil {
		return ResolvedSource{}, fmt.Errorf("prepare source %q at %q: %w", source.Name, ref, err)
	}

	if !packageExists(dir, category, pkgName) {
		return ResolvedSource{}, &SkippedError{Package: pkg, Reason: fmt.Sprintf("not found in %s", source.Name)}
	}

	sha, err := resolveHead(dir)
	if err != nil {
		return ResolvedSource{}, fmt.Errorf("resolve HEAD for %s: %w", source.Name, err)
	}

	return ResolvedSource{
		Name: source.Name,
		URL:  source.URL,
		Dir:  filepath.Join(dir, category, pkgName),
		Ref:  ref,
		SHA:  sha,
	}, nil
}

func (m *Manager) dirFor(ctx context.Context, source config.Source, ref string) (string, error) {
	if m.dirs[source.Name] == nil {
		m.dirs[source.Name] = make(map[string]string)
	}
	if d, ok := m.dirs[source.Name][ref]; ok {
		return d, nil
	}

	d := filepath.Join(m.base, source.Name, ref)
	if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
		return "", err
	}
	if err := m.cloner.Clone(ctx, sourceAtRef(source, ref), d); err != nil {
		return "", err
	}
	m.dirs[source.Name][ref] = d
	return d, nil
}

func sourceAtRef(s config.Source, ref string) config.Source {
	s.Ref = ref
	return s
}

func splitPackageID(id string) (string, string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid package id %q", id)
	}
	return parts[0], parts[1], nil
}

func packageExists(dir, category, pkg string) bool {
	info, err := os.Stat(filepath.Join(dir, category, pkg))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func resolveHead(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short=12", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// copyDir recursively copies src to dst, preserving symlinks. It is intended
// for test cloners that materialize fixture repositories.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
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
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
