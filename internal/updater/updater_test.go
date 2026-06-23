package updater

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/git"
	"github.com/s3-odara/gentoo-overlay/internal/github"
	"github.com/s3-odara/gentoo-overlay/internal/overlay"
	"github.com/s3-odara/gentoo-overlay/internal/source"
	"github.com/s3-odara/gentoo-overlay/internal/testutil"
)

// mockSourceResolver is a test source resolver.
type mockSourceResolver struct {
	sources  map[string]source.ResolvedSource
	excluded map[string]bool
	missing  map[string]bool
	err      error
}

func (m *mockSourceResolver) Resolve(ctx context.Context, pkg string) (source.ResolvedSource, error) {
	if m.err != nil {
		return source.ResolvedSource{}, m.err
	}
	if m.excluded[pkg] {
		return source.ResolvedSource{}, source.ErrExcluded
	}
	if src, ok := m.sources[pkg]; ok {
		return src, nil
	}
	return source.ResolvedSource{}, source.ErrNotFound
}

// mockDirSyncer copies srcDir to dstDir for tests.
type mockDirSyncer struct {
	calls []struct{ Src, Dst string }
	err   error
}

func (m *mockDirSyncer) SyncRepo(srcDir, dstDir string) error {
	m.calls = append(m.calls, struct{ Src, Dst string }{srcDir, dstDir})
	if m.err != nil {
		return m.err
	}
	// Unit tests often leave source directories virtual because the git driver
	// is mocked. Integration tests provide real directories and exercise the
	// actual file copy path.
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(dstDir); err != nil {
		return err
	}
	return testutil.CopyDir(srcDir, dstDir)
}

// overlayDirSyncer wires the production overlay.SyncRepo implementation into
// updater integration tests so the cleanup/base invariant is exercised with
// the real directory syncer.
type overlayDirSyncer struct{}

func (overlayDirSyncer) SyncRepo(srcDir, dstDir string) error {
	return overlay.SyncRepo(srcDir, dstDir)
}

// ensureDirSyncer is a test syncer that simply ensures the destination package
// directory exists. It lets unit tests with mocked git drivers exercise the
// manifest runner without depending on real source content.
type ensureDirSyncer struct{}

func (ensureDirSyncer) SyncRepo(_, dstDir string) error {
	return os.MkdirAll(dstDir, 0o755)
}

// mockCommandRunner records invocations and returns configured errors.
type mockCommandRunner struct {
	manifestCalls   []string
	pkgcheckCalls   []string
	manifestErr     error
	pkgcheckErr     error
	manifestErrFor  map[string]error
	pkgcheckErrFor  map[string]error
	touchManifest   bool
	manifestContent string
}

func (m *mockCommandRunner) RunManifest(repoDir, pkgPath string) error {
	m.manifestCalls = append(m.manifestCalls, pkgPath)
	if err, ok := m.manifestErrFor[pkgPath]; ok {
		return err
	}
	if m.touchManifest {
		manifestPath := filepath.Join(repoDir, pkgPath, "Manifest")
		content := m.manifestContent
		if content == "" {
			content = "DIST fake 0 BLAKE2B fake SHA512 fake\n"
		}
		if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return m.manifestErr
}

func (m *mockCommandRunner) RunPkgcheck(repoDir, pkgPath string) error {
	m.pkgcheckCalls = append(m.pkgcheckCalls, pkgPath)
	if err, ok := m.pkgcheckErrFor[pkgPath]; ok {
		return err
	}
	return m.pkgcheckErr
}

// mockPRClient records PR creation calls.
type mockPRClient struct {
	calls []github.PullRequest
	url   string
	err   error
}

func (m *mockPRClient) CreatePullRequest(ctx context.Context, owner, repo string, pr github.PullRequest) (string, error) {
	m.calls = append(m.calls, pr)
	if m.err != nil {
		return "", m.err
	}
	if m.url != "" {
		return m.url, nil
	}
	return "https://github.com/owner/repo/pull/1", nil
}

// mockGit is a programmable git driver for unit tests.
type mockGit struct {
	branches          []string
	branchBases       []string
	checkouts         []string
	resets            []string
	commits           []string
	pushes            []string
	staged            []string
	hasChanges        bool
	checkoutErr       error
	resetErr          error
	createBranchErr   error
	stageErr          error
	hasStagedErr      error
	commitErr         error
	pushErr           error
	resetFailAfter    int
	resetCallCount    int
	checkoutFailAfter int
	checkoutCallCount int
}

func (m *mockGit) CreateBranch(repoDir, branch, base string) error {
	m.branches = append(m.branches, branch)
	m.branchBases = append(m.branchBases, base)
	return m.createBranchErr
}

func (m *mockGit) Stage(repoDir string, paths ...string) error {
	if len(paths) > 0 {
		m.staged = append(m.staged, paths[0])
	}
	return m.stageErr
}

func (m *mockGit) HasStagedChanges(repoDir string, paths ...string) (bool, error) {
	return m.hasChanges, m.hasStagedErr
}

func (m *mockGit) Commit(repoDir, message string) error {
	m.commits = append(m.commits, message)
	return m.commitErr
}

func (m *mockGit) Push(repoDir, remote, branch string) error {
	m.pushes = append(m.pushes, branch)
	return m.pushErr
}

func (m *mockGit) Checkout(repoDir, branch string) error {
	m.checkoutCallCount++
	m.checkouts = append(m.checkouts, branch)
	if m.checkoutErr != nil {
		return m.checkoutErr
	}
	if m.checkoutFailAfter > 0 && m.checkoutCallCount > m.checkoutFailAfter {
		return errors.New("checkout failed after limit")
	}
	return nil
}

func (m *mockGit) ResetHard(repoDir string) error {
	m.resetCallCount++
	m.resets = append(m.resets, repoDir)
	if m.resetErr != nil {
		return m.resetErr
	}
	if m.resetFailAfter > 0 && m.resetCallCount > m.resetFailAfter {
		return errors.New("reset failed after limit")
	}
	return nil
}

func testConfig(g GitDriver, pr PRClient, src SourceResolver, sync DirSyncer, runner CommandRunner) RunConfig {
	return RunConfig{
		SourceResolver: src,
		Git:            g,
		PRClient:       pr,
		DirSyncer:      sync,
		CommandRunner:  runner,
		RootDir:        "/repo",
		Owner:          "owner",
		Repo:           "repo",
		BaseBranch:     "main",
		BranchPrefix:   "update",
	}
}

// pkg returns a minimal discovery.Package for the given category/package id.
func pkg(id, category, name string) discovery.Package {
	return discovery.Package{ID: id, Category: category, Name: name, Path: "/repo/" + id}
}

func TestRun_MissingSourceSkip(t *testing.T) {
	g := &mockGit{}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		missing: map[string]bool{"cat/foo": true},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got PRs=%v failures=%v", summary.Created, summary.Failures)
	}
	if len(g.branches) != 0 {
		t.Fatalf("expected no local branch created for missing source, got %v", g.branches)
	}
	if !strings.Contains(out.String(), "skip cat/foo") {
		t.Fatalf("output should report skip, got:\n%s", out.String())
	}
}

func TestRun_ExcludedSkip(t *testing.T) {
	g := &mockGit{}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		excluded: map[string]bool{"cat/foo": true},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got PRs=%v failures=%v", summary.Created, summary.Failures)
	}
	if !strings.Contains(out.String(), "skip cat/foo") {
		t.Fatalf("output should report skip, got:\n%s", out.String())
	}
}

func TestRun_DuplicateRemoteBranch(t *testing.T) {
	g := &mockGit{
		hasChanges: true,
		pushErr:    errors.New("remote rejected: already exists"),
	}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got PRs=%v failures=%v", summary.Created, summary.Failures)
	}
	if len(g.branches) != 1 {
		t.Fatalf("expected branch creation attempt, got %v", g.branches)
	}
	if len(g.pushes) != 1 {
		t.Fatalf("expected push attempt, got %v", g.pushes)
	}
	if len(pr.calls) != 0 {
		t.Fatalf("expected no PR for duplicate branch, got %v", pr.calls)
	}
	if !strings.Contains(out.String(), "skip cat/foo") {
		t.Fatalf("output should report duplicate branch skip, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Fatalf("output should mention already exists, got:\n%s", out.String())
	}
}

func TestRun_CreatesPRWithChanges(t *testing.T) {
	g := &mockGit{hasChanges: true}
	pr := &mockPRClient{url: "https://github.com/owner/repo/pull/42"}
	runner := &mockCommandRunner{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "deadbeef1234", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, runner)

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Created) != 1 {
		t.Fatalf("expected 1 PR, got %v", summary.Created)
	}
	created := summary.Created[0]
	wantBranch := "update/cat-foo/deadbeef1234"
	if created.Branch != wantBranch {
		t.Fatalf("expected branch %s, got %s", wantBranch, created.Branch)
	}
	if created.URL != "https://github.com/owner/repo/pull/42" {
		t.Fatalf("expected PR URL, got %s", created.URL)
	}
	if created.Source != "guru" {
		t.Fatalf("expected source guru, got %s", created.Source)
	}

	if len(g.branches) != 1 || g.branches[0] != wantBranch {
		t.Fatalf("unexpected branches: %v", g.branches)
	}
	if len(g.branchBases) != 1 || g.branchBases[0] != "main" {
		t.Fatalf("expected branch from main, got %v", g.branchBases)
	}
	if len(g.commits) != 1 || g.commits[0] != "sync cat/foo from guru" {
		t.Fatalf("unexpected commit: %v", g.commits)
	}
	if len(g.pushes) != 1 || g.pushes[0] != wantBranch {
		t.Fatalf("unexpected push: %v", g.pushes)
	}
	if len(runner.manifestCalls) != 1 || runner.manifestCalls[0] != "cat/foo" {
		t.Fatalf("expected manifest call for cat/foo, got %v", runner.manifestCalls)
	}
	if len(runner.pkgcheckCalls) != 1 || runner.pkgcheckCalls[0] != "cat/foo" {
		t.Fatalf("expected pkgcheck call for cat/foo, got %v", runner.pkgcheckCalls)
	}

	prCall := pr.calls[0]
	if prCall.Title != "Update cat/foo from guru" {
		t.Fatalf("unexpected PR title: %q", prCall.Title)
	}
	if prCall.Head != wantBranch || prCall.Base != "main" {
		t.Fatalf("unexpected PR head/base: %q/%q", prCall.Head, prCall.Base)
	}
	for _, want := range []string{"guru", "https://guru", "deadbeef1234"} {
		if !strings.Contains(prCall.Body, want) {
			t.Fatalf("PR body missing %q:\n%s", want, prCall.Body)
		}
	}
}

func TestRun_NoPRWhenNoDiff(t *testing.T) {
	g := &mockGit{hasChanges: false}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got %v %v", summary.Created, summary.Failures)
	}
	if len(g.commits) != 0 || len(g.pushes) != 0 || len(pr.calls) != 0 {
		t.Fatalf("expected no commit/push/PR for up-to-date package")
	}
	if !strings.Contains(out.String(), "skip cat/foo: up to date") {
		t.Fatalf("output should say up to date, got:\n%s", out.String())
	}
}

func TestRun_ManifestFailureBlocksPR(t *testing.T) {
	g := &mockGit{hasChanges: true}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{manifestErr: errors.New("distfile fetch failed")})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error for manifest failure")
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Phase != "manifest" {
		t.Fatalf("expected manifest failure, got %v", summary.Failures)
	}
	if len(summary.Created) != 0 || len(pr.calls) != 0 {
		t.Fatalf("expected no PR after manifest failure")
	}
	if len(g.commits) != 0 || len(g.pushes) != 0 {
		t.Fatalf("expected no commit/push after manifest failure")
	}
}

func TestRun_PkgcheckFailureBlocksPR(t *testing.T) {
	g := &mockGit{hasChanges: true}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{pkgcheckErr: errors.New("pkgcheck error")})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error for pkgcheck failure")
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Phase != "pkgcheck" {
		t.Fatalf("expected pkgcheck failure, got %v", summary.Failures)
	}
	if len(summary.Created) != 0 || len(pr.calls) != 0 {
		t.Fatalf("expected no PR after pkgcheck failure")
	}
}

func TestRun_AggregateFailureContinues(t *testing.T) {
	g := &mockGit{hasChanges: true}
	pr := &mockPRClient{}
	src := &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "bad", Dir: "/src/cat/foo"},
			"cat/bar": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "good", Dir: "/src/cat/bar"},
		},
	}
	runner := &mockCommandRunner{manifestErrFor: map[string]error{"cat/foo": errors.New("fail foo")}}
	cfg := testConfig(g, pr, src, &mockDirSyncer{}, runner)

	pkgs := []discovery.Package{
		{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"},
		{ID: "cat/bar", Category: "cat", Name: "bar", Path: "/repo/cat/bar"},
	}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if len(summary.Created) != 1 || summary.Created[0].Package != "cat/bar" {
		t.Fatalf("expected PR for cat/bar, got %v", summary.Created)
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Package != "cat/foo" {
		t.Fatalf("expected failure for cat/foo, got %v", summary.Failures)
	}
}

func TestRun_CleanupFailure(t *testing.T) {
	tests := []struct {
		name        string
		pkgs        []discovery.Package
		runner      CommandRunner
		wantCreated int
		wantFirst   string
		wantPhases  []string
	}{
		{
			name:        "surfaces after successful PR",
			pkgs:        []discovery.Package{pkg("cat/foo", "cat", "foo")},
			runner:      &mockCommandRunner{touchManifest: true},
			wantCreated: 1,
			wantPhases:  []string{"cleanup"},
		},
		{
			name: "stops run after first failure",
			pkgs: []discovery.Package{
				pkg("cat/foo", "cat", "foo"),
				pkg("cat/bar", "cat", "bar"),
			},
			runner:      &mockCommandRunner{touchManifest: true},
			wantCreated: 1,
			wantFirst:   "cat/bar",
			wantPhases:  []string{"cleanup"},
		},
		{
			name:        "surfaces alongside gate failure",
			pkgs:        []discovery.Package{pkg("cat/foo", "cat", "foo")},
			runner:      &mockCommandRunner{manifestErr: errors.New("distfile fetch failed")},
			wantCreated: 0,
			wantPhases:  []string{"manifest", "cleanup"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &mockGit{
				hasChanges:     true,
				resetFailAfter: 1,
			}
			pr := &mockPRClient{url: "https://github.com/owner/repo/pull/42"}
			src := &mockSourceResolver{
				sources: map[string]source.ResolvedSource{
					"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "deadbeef1234", Dir: "/src/cat/foo"},
					"cat/bar": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "barsha123456", Dir: "/src/cat/bar"},
				},
			}
			cfg := testConfig(g, pr, src, ensureDirSyncer{}, tt.runner)
			cfg.RootDir = t.TempDir()
			for i := range tt.pkgs {
				tt.pkgs[i].Path = filepath.Join(cfg.RootDir, tt.pkgs[i].ID)
			}

			summary, err := Run(context.Background(), cfg, tt.pkgs, io.Discard)
			if err == nil {
				t.Fatal("expected aggregate error")
			}
			if len(summary.Created) != tt.wantCreated {
				t.Fatalf("created: got %v, want %d", summary.Created, tt.wantCreated)
			}
			if tt.wantFirst != "" && (len(summary.Created) == 0 || summary.Created[0].Package != tt.wantFirst) {
				t.Fatalf("first created: got %v, want %s", summary.Created, tt.wantFirst)
			}
			phases := map[string]bool{}
			for _, f := range summary.Failures {
				phases[f.Phase] = true
			}
			for _, phase := range tt.wantPhases {
				if !phases[phase] {
					t.Fatalf("expected %s failure, got %v", phase, summary.Failures)
				}
			}
		})
	}
}

// noopPushGit delegates git operations to the real ExecDriver but records and
// suppresses pushes so integration tests do not need a network remote.
type noopPushGit struct {
	*git.ExecDriver
	pushes []string
}

func (n *noopPushGit) Push(repoDir, remote, branch string) error {
	n.pushes = append(n.pushes, branch)
	return nil
}

func TestRun_CheckoutFailureRunsCleanup(t *testing.T) {
	g := &mockGit{checkoutErr: errors.New("checkout failed")}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}

	phases := map[string]bool{}
	for _, f := range summary.Failures {
		phases[f.Phase] = true
	}
	if !phases["git-checkout"] {
		t.Fatalf("expected git-checkout failure, got %v", summary.Failures)
	}
	if !phases["cleanup"] {
		t.Fatalf("expected cleanup failure after checkout failed, got %v", summary.Failures)
	}
	if len(g.resets) == 0 {
		t.Fatal("expected cleanup reset after checkout failure")
	}
}

func TestRun_ResetFailureRunsCleanup(t *testing.T) {
	g := &mockGit{resetErr: errors.New("reset failed")}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc123", Dir: "/src/cat/foo"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}

	phases := map[string]bool{}
	for _, f := range summary.Failures {
		phases[f.Phase] = true
	}
	if !phases["git-reset"] {
		t.Fatalf("expected git-reset failure, got %v", summary.Failures)
	}
	if !phases["cleanup"] {
		t.Fatalf("expected cleanup failure after reset failed, got %v", summary.Failures)
	}
	if len(g.resets) < 2 {
		t.Fatalf("expected initial reset and cleanup reset, got %v", g.resets)
	}
}

func TestRun_CleanupIsolation(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitGitRepo(t, tmp)

	// Two packages on main.
	testutil.WriteFiles(t, tmp, map[string]string{
		"cat/foo/foo.ebuild": "old foo\n",
		"cat/bar/bar.ebuild": "old bar\n",
		"cat/bar/only-local": "keep?\n",
	})
	testutil.RunGit(t, tmp, "add", ".")
	testutil.RunGit(t, tmp, "commit", "-m", "add packages", "--quiet")

	// Provide an origin remote so pushes can be recorded without contacting the
	// network. The bare repo starts with no branches.
	origin := t.TempDir()
	testutil.RunGit(t, origin, "init", "--bare", "--quiet")
	testutil.RunGit(t, tmp, "remote", "add", "origin", origin)

	// Source directories with updated content.
	fooSrc := t.TempDir()
	testutil.WriteFiles(t, fooSrc, map[string]string{"foo.ebuild": "new foo\n"})
	barSrc := t.TempDir()
	testutil.WriteFiles(t, barSrc, map[string]string{"bar.ebuild": "new bar\n"})

	// Source resolver uses deterministic SHAs so branch names are predictable.
	src := &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "foosha123456", Dir: fooSrc},
			"cat/bar": {Name: "gentoo-zh", URL: "https://zh", Ref: "master", SHA: "barsha123456", Dir: barSrc},
		},
	}

	g := &noopPushGit{ExecDriver: &git.ExecDriver{}}
	cfg := RunConfig{
		SourceResolver: src,
		Git:            g,
		PRClient:       &mockPRClient{},
		DirSyncer:      overlayDirSyncer{},
		CommandRunner:  &mockCommandRunner{touchManifest: true},
		RootDir:        tmp,
		Owner:          "owner",
		Repo:           "repo",
		BaseBranch:     "main",
		BranchPrefix:   "update",
	}

	pkgs := []discovery.Package{
		{ID: "cat/bar", Category: "cat", Name: "bar", Path: filepath.Join(tmp, "cat/bar")},
		{ID: "cat/foo", Category: "cat", Name: "foo", Path: filepath.Join(tmp, "cat/foo")},
	}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(summary.Created) != 2 {
		t.Fatalf("expected 2 PRs, got %v", summary.Created)
	}

	// Each branch should only contain its own package's changes.
	for _, pkg := range []string{"foo", "bar"} {
		var branch string
		for _, pr := range summary.Created {
			if pr.Package == "cat/"+pkg {
				branch = pr.Branch
				break
			}
		}
		if branch == "" {
			t.Fatalf("missing branch for cat/%s", pkg)
		}
		out, err := exec.Command("git", "-C", tmp, "diff", "main.."+branch, "--name-only").CombinedOutput()
		if err != nil {
			t.Fatalf("diff %s: %v\n%s", branch, err, out)
		}
		files := strings.Fields(string(out))
		wantPrefix := "cat/" + pkg + "/"
		wantEbuild := wantPrefix + pkg + ".ebuild"
		if len(files) == 0 {
			t.Fatalf("%s should contain changes, got none", branch)
		}
		for _, f := range files {
			if !strings.HasPrefix(f, wantPrefix) {
				t.Fatalf("%s contains file from another package: %s", branch, f)
			}
		}
		found := false
		for _, f := range files {
			if f == wantEbuild {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s missing %s, got %v", branch, wantEbuild, files)
		}
	}

	// The repository should be back on main with a clean worktree.
	out, err := exec.Command("git", "-C", tmp, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("show current branch: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("expected to end on main, got %q", string(out))
	}
	out, err = exec.Command("git", "-C", tmp, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected clean worktree, got:\n%s", string(out))
	}

	// Local files removed by full sync should be gone from the update branch
	// (verified above by diff), but the main branch still has them.
	if _, err := os.Stat(filepath.Join(tmp, "cat/bar", "only-local")); err != nil {
		t.Fatalf("main branch file removed by sync should still exist on main: %v", err)
	}
}
