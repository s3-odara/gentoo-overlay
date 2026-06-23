package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/s3-odara/gentoo-overlay/internal/git"
)

// ErrExcluded is returned by Resolve when the package is excluded from
// automatic updates.
var ErrExcluded = errors.New("package is excluded")

// ErrNotFound is returned by Resolve when the package is not present in any
// configured source overlay.
var ErrNotFound = errors.New("package not found in any configured source")

// Cloner obtains a local clone of a source overlay.
type Cloner interface {
	Clone(ctx context.Context, source Source, dst string) error
}

// GitCloner clones source overlays with git using a shallow clone at the
// source's configured ref.
type GitCloner struct{}

// Clone performs a shallow clone of source.URL at source.Ref into dst.
func (g *GitCloner) Clone(ctx context.Context, source Source, dst string) error {
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
	Dir  string // package directory in the source overlay
	Ref  string
	SHA  string // resolved commit SHA
}

// Manager queries source overlays for package lookup.
type Manager struct {
	cloner Cloner
	base   string
	// clones maps source name to its local clone directory.
	clones map[string]string
}

// NewManager returns a manager that will clone sources under baseDir.
func NewManager(cloner Cloner, baseDir string) *Manager {
	return &Manager{
		cloner: cloner,
		base:   baseDir,
		clones: make(map[string]string),
	}
}

// Prepare clones all configured sources once. It must be called before Resolve.
func (m *Manager) Prepare(ctx context.Context) error {
	for _, s := range sources {
		d := filepath.Join(m.base, s.Name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("prepare source dir for %q: %w", s.Name, err)
		}
		if err := m.cloner.Clone(ctx, s, d); err != nil {
			return fmt.Errorf("prepare source %q: %w", s.Name, err)
		}
		m.clones[s.Name] = d
	}
	return nil
}

// Resolve selects the first source overlay (in priority order) that contains
// pkg and returns its resolved commit SHA at the source's configured ref. It
// returns ErrExcluded for excluded packages and ErrNotFound when no source has
// the package.
//
// The context is intentionally ignored: all network I/O (cloning) happens in
// Prepare, and Resolve performs only local filesystem checks and a single
// git.ResolveHead subprocess call. The parameter is kept to satisfy the
// SourceResolver interface used by the updater.
func (m *Manager) Resolve(_ context.Context, pkg string) (ResolvedSource, error) {
	if isExcluded(pkg) {
		return ResolvedSource{}, fmt.Errorf("%w: %s", ErrExcluded, pkg)
	}

	category, pkgName, err := splitPackageID(pkg)
	if err != nil {
		return ResolvedSource{}, err
	}

	for _, s := range sources {
		dir, ok := m.clones[s.Name]
		if !ok {
			return ResolvedSource{}, fmt.Errorf("source %q not prepared", s.Name)
		}
		if !packageExists(dir, category, pkgName) {
			continue
		}
		sha, err := git.ResolveHead(dir)
		if err != nil {
			return ResolvedSource{}, fmt.Errorf("resolve HEAD for %s: %w", s.Name, err)
		}
		return ResolvedSource{
			Name: s.Name,
			URL:  s.URL,
			Dir:  filepath.Join(dir, category, pkgName),
			Ref:  s.Ref,
			SHA:  sha,
		}, nil
	}

	return ResolvedSource{}, fmt.Errorf("%w: %s", ErrNotFound, pkg)
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
