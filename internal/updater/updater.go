package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/github"
	"github.com/s3-odara/gentoo-overlay/internal/source"
)

// GitDriver is the subset of git operations needed by the updater.
// The production implementation is internal/git.ExecDriver.
type GitDriver interface {
	CreateBranch(repoDir, branch, base string) error
	Stage(repoDir string, paths ...string) error
	HasStagedChanges(repoDir string, paths ...string) (bool, error)
	Commit(repoDir, message string) error
	Push(repoDir, remote, branch string) error
	Checkout(repoDir, branch string) error
	ResetHard(repoDir string) error
}

// PRClient creates pull requests. The production implementation is
// internal/github.Client.
type PRClient interface {
	CreatePullRequest(ctx context.Context, owner, repo string, pr github.PullRequest) (string, error)
}

// SourceResolver selects the upstream source overlay for a package and reports
// its resolved commit SHA. The production implementation is internal/source.Manager.
type SourceResolver interface {
	Resolve(ctx context.Context, pkg string) (source.ResolvedSource, error)
}

// DirSyncer replaces the local package directory with the selected upstream
// package directory. The production implementation is internal/overlay.SyncRepo;
// srcDir is the resolved upstream package directory, which need not be a git
// repository root.
type DirSyncer interface {
	SyncRepo(srcDir, dstDir string) error
}

// CommandRunner runs the external Gentoo tooling gates. Tests provide a mock
// implementation so the updater can be exercised without pkgdev/pkgcheck.
type CommandRunner interface {
	RunManifest(repoDir, pkgPath string) error
	RunPkgcheck(repoDir, pkgPath string) error
}

// RunConfig wires the external dependencies of the updater.
type RunConfig struct {
	SourceResolver SourceResolver
	Git            GitDriver
	PRClient       PRClient
	DirSyncer      DirSyncer
	CommandRunner  CommandRunner
	RootDir        string
	Owner          string
	Repo           string
	BaseBranch     string
	BranchPrefix   string
}

// PRCreated records a successfully created pull request.
type PRCreated struct {
	Package string
	Branch  string
	URL     string
	Source  string
}

// Failure records a package that failed one of the update gates.
type Failure struct {
	Package string
	Phase   string
	Err     error
}

// RunSummary records the result of an updater run.
type RunSummary struct {
	Created  []PRCreated
	Failures []Failure
}

// HasFailures reports whether any package failed a gate or API call.
func (s *RunSummary) HasFailures() bool {
	return len(s.Failures) > 0
}

// Error returns an aggregate error for all recorded failures.
func (s *RunSummary) Error() error {
	if !s.HasFailures() {
		return nil
	}
	errs := make([]error, len(s.Failures))
	for i, f := range s.Failures {
		errs[i] = fmt.Errorf("%s: %s: %w", f.Package, f.Phase, f.Err)
	}
	return errors.Join(errs...)
}

// Run processes discovered packages deterministically and creates one pull
// request per changed package after full sync, Manifest regeneration, and QA.
// It always attempts every package and returns an aggregate error if any fail.
func Run(ctx context.Context, cfg RunConfig, pkgs []discovery.Package, out io.Writer) (*RunSummary, error) {
	if out == nil {
		out = io.Discard
	}

	pkgs = sortPackages(pkgs)
	summary := &RunSummary{}

	for _, pkg := range pkgs {
		if err := processPackage(ctx, cfg, pkg, out, summary); err != nil {
			// processPackage records failures in the summary; an error returned
			// here means cleanup itself failed and we should stop before the
			// worktree becomes inconsistent.
			return summary, err
		}
	}

	PrintSummary(out, summary)
	return summary, summary.Error()
}

func sortPackages(pkgs []discovery.Package) []discovery.Package {
	sorted := make([]discovery.Package, len(pkgs))
	copy(sorted, pkgs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted
}

func processPackage(ctx context.Context, cfg RunConfig, pkg discovery.Package, out io.Writer, summary *RunSummary) (rerr error) {
	fmt.Fprintf(out, "Checking %s\n", pkg.ID)

	src, err := cfg.SourceResolver.Resolve(ctx, pkg.ID)
	if err != nil {
		if errors.Is(err, source.ErrExcluded) || errors.Is(err, source.ErrNotFound) {
			fmt.Fprintf(out, "skip %s: %v\n", pkg.ID, err)
			return nil
		}
		recordFailure(summary, pkg.ID, "source", err)
		return nil
	}

	branch := branchName(cfg.BranchPrefix, pkg, src.SHA)

	// Mutation begins here. Register the cleanup guard before the first
	// worktree-mutating operation so that even a preparatory checkout/reset
	// failure restores the base branch and a clean worktree.
	cleanup := func() {
		var errs []error
		if err := cfg.Git.ResetHard(cfg.RootDir); err != nil {
			errs = append(errs, fmt.Errorf("reset: %w", err))
		}
		if err := cfg.Git.Checkout(cfg.RootDir, cfg.BaseBranch); err != nil {
			errs = append(errs, fmt.Errorf("checkout: %w", err))
		}
		if len(errs) > 0 {
			err := errors.Join(errs...)
			fmt.Fprintf(out, "Cleanup failed for %s: %v\n", pkg.ID, err)
			rerr = recordFailure(summary, pkg.ID, "cleanup", err)
		}
	}
	defer cleanup()

	if err := cfg.Git.Checkout(cfg.RootDir, cfg.BaseBranch); err != nil {
		return recordFailure(summary, pkg.ID, "git-checkout", err)
	}
	if err := cfg.Git.ResetHard(cfg.RootDir); err != nil {
		return recordFailure(summary, pkg.ID, "git-reset", err)
	}

	if err := cfg.Git.CreateBranch(cfg.RootDir, branch, cfg.BaseBranch); err != nil {
		recordFailure(summary, pkg.ID, "git-branch", err)
		return nil
	}

	localDir := filepath.Join(cfg.RootDir, pkg.Category, pkg.Name)
	if err := cfg.DirSyncer.SyncRepo(src.Dir, localDir); err != nil {
		recordFailure(summary, pkg.ID, "sync", err)
		return nil
	}

	pkgRelPath := filepath.Join(pkg.Category, pkg.Name)
	if err := cfg.Git.Stage(cfg.RootDir, pkgRelPath); err != nil {
		recordFailure(summary, pkg.ID, "git-stage", err)
		return nil
	}

	// Regenerate the local Manifest; do not treat the upstream Manifest as
	// authoritative even though it was copied during the full sync.
	if err := cfg.CommandRunner.RunManifest(cfg.RootDir, pkgRelPath); err != nil {
		recordFailure(summary, pkg.ID, "manifest", err)
		return nil
	}
	if err := cfg.Git.Stage(cfg.RootDir, filepath.Join(pkgRelPath, "Manifest")); err != nil {
		recordFailure(summary, pkg.ID, "git-stage-manifest", err)
		return nil
	}

	if err := cfg.CommandRunner.RunPkgcheck(cfg.RootDir, pkgRelPath); err != nil {
		recordFailure(summary, pkg.ID, "pkgcheck", err)
		return nil
	}

	changed, err := cfg.Git.HasStagedChanges(cfg.RootDir, pkgRelPath)
	if err != nil {
		recordFailure(summary, pkg.ID, "git-diff", err)
		return nil
	}
	if !changed {
		fmt.Fprintf(out, "skip %s: up to date\n", pkg.ID)
		return nil
	}

	fmt.Fprintf(out, "  %s: changes detected\n", pkg.ID)

	if err := cfg.Git.Commit(cfg.RootDir, commitMessage(pkg, src)); err != nil {
		recordFailure(summary, pkg.ID, "git-commit", err)
		return nil
	}

	if err := cfg.Git.Push(cfg.RootDir, "origin", branch); err != nil {
		msg := strings.ToLower(err.Error())
		// Only genuine duplicate-branch or upstream-divergence signals are
		// treated as soft skips. A generic "rejected" match would swallow real
		// failures such as hook-declined pushes, which must be recorded.
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "non-fast-forward") {
			fmt.Fprintf(out, "skip %s: %v\n", pkg.ID, err)
			return nil
		}
		recordFailure(summary, pkg.ID, "git-push", err)
		return nil
	}

	pr := github.PullRequest{
		Title: prTitle(pkg, src),
		Head:  branch,
		Base:  cfg.BaseBranch,
		Body:  BuildPRBody(pkg, src),
	}
	url, err := cfg.PRClient.CreatePullRequest(ctx, cfg.Owner, cfg.Repo, pr)
	if err != nil {
		recordFailure(summary, pkg.ID, "github", err)
		return nil
	}

	fmt.Fprintf(out, "  created PR: %s\n", url)
	summary.Created = append(summary.Created, PRCreated{
		Package: pkg.ID,
		Branch:  branch,
		URL:     url,
		Source:  src.Name,
	})
	return nil
}

func recordFailure(summary *RunSummary, pkg, phase string, err error) error {
	summary.Failures = append(summary.Failures, Failure{Package: pkg, Phase: phase, Err: err})
	return err
}

func branchName(prefix string, pkg discovery.Package, sha string) string {
	if strings.TrimSpace(prefix) == "" {
		prefix = source.BranchPrefix
	}
	return fmt.Sprintf("%s/%s-%s/%s", prefix, pkg.Category, pkg.Name, sha)
}

func commitMessage(pkg discovery.Package, src source.ResolvedSource) string {
	return fmt.Sprintf("sync %s from %s", pkg.ID, src.Name)
}

func prTitle(pkg discovery.Package, src source.ResolvedSource) string {
	return fmt.Sprintf("Update %s from %s", pkg.ID, src.Name)
}
