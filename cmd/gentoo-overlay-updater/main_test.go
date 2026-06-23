package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/git"
	"github.com/s3-odara/gentoo-overlay/internal/github"
	"github.com/s3-odara/gentoo-overlay/internal/source"
	"github.com/s3-odara/gentoo-overlay/internal/updater"
)

func TestParseArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, root, base, help, err := parseArgs(nil, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if cfg != defaultConfig {
			t.Fatalf("config path: got %q, want %q", cfg, defaultConfig)
		}
		if root != "." {
			t.Fatalf("root: got %q, want %q", root, ".")
		}
		if base != "" {
			t.Fatalf("base branch flag: got %q, want empty", base)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg, root, base, help, err := parseArgs([]string{
			"-config", "custom.json",
			"-root", "/tmp/overlay",
			"-base-branch", "dev",
		}, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if cfg != "custom.json" || root != "/tmp/overlay" || base != "dev" {
			t.Fatalf("unexpected flags: config=%q root=%q base=%q", cfg, root, base)
		}
	})

	t.Run("rejects positional arguments", func(t *testing.T) {
		_, _, _, _, err := parseArgs([]string{"extra"}, io.Discard)
		if err == nil {
			t.Fatal("expected error for positional argument")
		}
		if !strings.Contains(err.Error(), "positional argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("help is not an error", func(t *testing.T) {
		var buf strings.Builder
		cfg, root, base, help, err := parseArgs([]string{"-help"}, &buf)
		if err != nil {
			t.Fatalf("parseArgs -help: %v", err)
		}
		if !help {
			t.Fatal("expected help request")
		}
		// The default values are returned so run can exit cleanly after printing
		// usage, but the caller is expected to stop processing on help.
		if cfg != defaultConfig || root != "." || base != "" {
			t.Fatalf("unexpected flag values after help: %q %q %q", cfg, root, base)
		}
		if !strings.Contains(buf.String(), "Usage") {
			t.Fatalf("help output missing usage: %q", buf.String())
		}
	})
}

func TestResolveBaseBranch(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		got := resolveBaseBranch("dev", func(string) string { return "main" })
		if got != "dev" {
			t.Fatalf("got %q, want dev", got)
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		got := resolveBaseBranch("", func(k string) string {
			if k == "GITHUB_REF_NAME" {
				return "ci-branch"
			}
			return ""
		})
		if got != "ci-branch" {
			t.Fatalf("got %q, want ci-branch", got)
		}
	})

	t.Run("default main", func(t *testing.T) {
		got := resolveBaseBranch("", func(string) string { return "" })
		if got != "main" {
			t.Fatalf("got %q, want main", got)
		}
	})
}

func TestParsePackageFilter(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := parsePackageFilter("")
		if err != nil {
			t.Fatalf("parsePackageFilter: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty filter, got %v", got)
		}
	})

	t.Run("valid csv", func(t *testing.T) {
		got, err := parsePackageFilter("gui-apps/fuzzel, app-misc/lf ,gui-wm/river")
		if err != nil {
			t.Fatalf("parsePackageFilter: %v", err)
		}
		want := []string{"gui-apps/fuzzel", "app-misc/lf", "gui-wm/river"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i, v := range want {
			if got[i] != v {
				t.Fatalf("filter[%d]: got %q, want %q", i, got[i], v)
			}
		}
	})

	t.Run("invalid package id", func(t *testing.T) {
		_, err := parsePackageFilter("not-a-package")
		if err == nil {
			t.Fatal("expected error for invalid package id")
		}
		if !strings.Contains(err.Error(), "expected category/package") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("deduplicates", func(t *testing.T) {
		got, err := parsePackageFilter("a/b, a/b")
		if err != nil {
			t.Fatalf("parsePackageFilter: %v", err)
		}
		if len(got) != 1 || got[0] != "a/b" {
			t.Fatalf("got %v, want [a/b]", got)
		}
	})
}

func TestRun_MissingToken(t *testing.T) {
	env := func(k string) string {
		if k == "GITHUB_REPOSITORY" {
			return "owner/repo"
		}
		return ""
	}
	err := run(context.Background(), io.Discard, nil, env)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_InvalidRepository(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "GITHUB_TOKEN":
			return "token"
		case "GITHUB_REPOSITORY":
			return "not-owner-slash-repo"
		}
		return ""
	}
	err := run(context.Background(), io.Discard, nil, env)
	if err == nil {
		t.Fatal("expected error for invalid repository")
	}
	if !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_Help(t *testing.T) {
	var buf strings.Builder
	err := run(context.Background(), &buf, []string{"-help"}, nil)
	if err != nil {
		t.Fatalf("run -help: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage") {
		t.Fatalf("help output missing usage: %q", buf.String())
	}
}

// TestFilterRespectsExclusions verifies that a package listed in the manual
// filter but excluded by configuration is skipped rather than processed. The
// fake source resolver below plays the role of the real source.Manager which
// consults config exclusions.
func TestFilterRespectsExclusions(t *testing.T) {
	excluded := "virtual/notification-daemon"
	other := "app-misc/lf"

	called := make(map[string]bool)
	fakeSource := &fakeSourceResolver{
		resolve: func(pkg string) (source.ResolvedSource, error) {
			called[pkg] = true
			if pkg == excluded {
				return source.ResolvedSource{}, &source.SkippedError{Package: pkg, Reason: "excluded"}
			}
			// Return a source with no directory so the sync fails deterministically;
			// the only thing we care about here is that the excluded package is
			// recorded as excluded and the other package is attempted.
			return source.ResolvedSource{}, &source.SkippedError{Package: pkg, Reason: "not found"}
		},
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "virtual", "notification-daemon"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "virtual", "notification-daemon", "notification-daemon-1.ebuild"), []byte("#\n"), 0o644); err != nil {
		t.Fatalf("write ebuild: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app-misc", "lf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app-misc", "lf", "lf-1.ebuild"), []byte("#\n"), 0o644); err != nil {
		t.Fatalf("write ebuild: %v", err)
	}

	pkgs, err := discovery.DiscoverPackages(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	cfg := updater.RunConfig{
		SourceResolver: fakeSource,
		Git:            &fakeGitDriver{},
		PRClient:       &fakePRClient{},
		DirSyncer:      fsSyncer{},
		CommandRunner:  &fakeCommandRunner{},
		RootDir:        root,
		Owner:          "owner",
		Repo:           "repo",
		BaseBranch:     "main",
		BranchPrefix:   "update",
		Filter:         []string{excluded, other},
	}

	summary, err := updater.Run(context.Background(), cfg, pkgs, io.Discard)
	if err != nil {
		// The aggregate error is expected because the non-excluded package is
		// reported missing; we still validate the summary below.
		t.Logf("aggregate error (expected): %v", err)
	}

	if !called[other] {
		t.Fatalf("expected %s to be resolved", other)
	}
	// The filter includes the excluded package, so it is still passed to the
	// source resolver; the exclusion is enforced by the resolver/config rather
	// than by dropping it from the filter.
	if !called[excluded] {
		t.Fatalf("expected %s to be passed to the source resolver", excluded)
	}

	foundExcluded := false
	for _, e := range summary.Excluded {
		if e == excluded {
			foundExcluded = true
			break
		}
	}
	if !foundExcluded {
		t.Fatalf("expected %s in summary.Excluded, got %v", excluded, summary.Excluded)
	}
}

func TestWriteSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	summary := &updater.RunSummary{
		Created: []updater.PRCreated{
			{Package: "app-misc/lf", Branch: "update/app-misc-lf/abc123", URL: "https://example.com/pr/1", Source: "guru"},
		},
	}
	if err := writeSummary(summary, path); err != nil {
		t.Fatalf("writeSummary: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(data), "app-misc/lf") {
		t.Fatalf("summary missing package: %s", data)
	}
	if !strings.Contains(string(data), "Created pull requests") {
		t.Fatalf("summary missing header: %s", data)
	}
}

type fakeSourceResolver struct {
	resolve func(string) (source.ResolvedSource, error)
}

func (f *fakeSourceResolver) Resolve(ctx context.Context, pkg string) (source.ResolvedSource, error) {
	return f.resolve(pkg)
}

type fakePRClient struct{}

func (f *fakePRClient) CreatePullRequest(ctx context.Context, owner, repo string, pr github.PullRequest) (string, error) {
	return "", errors.New("not implemented")
}

type fakeCommandRunner struct{}

func (f *fakeCommandRunner) RunManifest(repoDir, pkgPath string) error { return nil }
func (f *fakeCommandRunner) RunPkgcheck(repoDir, pkgPath string) error { return nil }

type fakeGitDriver struct{}

func (f *fakeGitDriver) CreateBranch(repoDir, branch, base string) error { return nil }
func (f *fakeGitDriver) Stage(repoDir string, paths ...string) error     { return nil }
func (f *fakeGitDriver) HasStagedChanges(repoDir string, paths ...string) (bool, error) {
	return false, nil
}
func (f *fakeGitDriver) StagedChanges(repoDir string, paths ...string) ([]git.Change, error) {
	return nil, nil
}
func (f *fakeGitDriver) Commit(repoDir, message string) error      { return nil }
func (f *fakeGitDriver) Push(repoDir, remote, branch string) error { return nil }
func (f *fakeGitDriver) Checkout(repoDir, branch string) error     { return nil }
func (f *fakeGitDriver) ResetHard(repoDir string) error            { return nil }
func (f *fakeGitDriver) RemoteBranchExists(repoDir, remote, branch string) (bool, error) {
	return false, nil
}
