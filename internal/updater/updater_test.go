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
)

// mockSourceResolver is a test source resolver.
type mockSourceResolver struct {
	sources map[string]source.ResolvedSource
	skipped map[string]*source.SkippedError
	err     error
}

func (m *mockSourceResolver) Resolve(ctx context.Context, pkg string) (source.ResolvedSource, error) {
	if m.err != nil {
		return source.ResolvedSource{}, m.err
	}
	if skipped, ok := m.skipped[pkg]; ok {
		return source.ResolvedSource{}, skipped
	}
	if src, ok := m.sources[pkg]; ok {
		return src, nil
	}
	return source.ResolvedSource{}, &source.SkippedError{Package: pkg, Reason: "package not found in any configured source"}
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
	return copyDir(srcDir, dstDir)
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
	remoteBranches    map[string]bool
	changes           []git.Change
	hasChanges        bool
	checkoutErr       error
	resetErr          error
	createBranchErr   error
	stageErr          error
	hasStagedErr      error
	stagedChangesErr  error
	commitErr         error
	pushErr           error
	remoteBranchErr   error
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

func (m *mockGit) StagedChanges(repoDir string, paths ...string) ([]git.Change, error) {
	return m.changes, m.stagedChangesErr
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

func (m *mockGit) RemoteBranchExists(repoDir, remote, branch string) (bool, error) {
	if m.remoteBranchErr != nil {
		return false, m.remoteBranchErr
	}
	return m.remoteBranches[branch], nil
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

func TestRun_MissingSourceSkip(t *testing.T) {
	g := &mockGit{}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		skipped: map[string]*source.SkippedError{
			"cat/foo": {Package: "cat/foo", Reason: "package not found in any configured source"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: "/repo/cat/foo"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.MissingSource) != 1 || summary.MissingSource[0] != "cat/foo" {
		t.Fatalf("expected missing source, got %v", summary.MissingSource)
	}
	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got PRs=%v failures=%v", summary.Created, summary.Failures)
	}
	if len(g.branches) != 0 {
		t.Fatalf("expected no local branch created for missing source, got %v", g.branches)
	}
}

func TestRun_DuplicateRemoteBranch(t *testing.T) {
	g := &mockGit{
		remoteBranches: map[string]bool{"update/cat-foo/abc123": true},
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

	if len(summary.AlreadyCovered) != 1 || summary.AlreadyCovered[0] != "cat/foo" {
		t.Fatalf("expected already covered, got %v", summary.AlreadyCovered)
	}
	if len(g.branches) != 0 || len(g.commits) != 0 || len(g.pushes) != 0 {
		t.Fatalf("expected no branch/commit/push for duplicate, branches=%v commits=%v pushes=%v", g.branches, g.commits, g.pushes)
	}
	if len(pr.calls) != 0 {
		t.Fatalf("expected no PR for duplicate branch, got %v", pr.calls)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Fatalf("output should report duplicate branch skip, got:\n%s", out.String())
	}
}

func TestRun_CreatesPRWithChanges(t *testing.T) {
	changes := []git.Change{
		{Path: "cat/foo/foo.ebuild", Status: git.Modified},
		{Path: "cat/foo/files/fix.patch", Status: git.Deleted},
		{Path: "cat/foo/new.file", Status: git.Added},
	}
	g := &mockGit{hasChanges: true, changes: changes}
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
	for _, want := range []string{"guru", "https://guru", "deadbeef1234", wantBranch, "modified: `foo.ebuild`", "deleted: `files/fix.patch`", "added: `new.file`"} {
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

	if len(summary.UpToDate) != 1 || summary.UpToDate[0] != "cat/foo" {
		t.Fatalf("expected up to date, got %v", summary.UpToDate)
	}
	if len(summary.Created) != 0 || len(summary.Failures) != 0 {
		t.Fatalf("expected no PRs or failures, got %v %v", summary.Created, summary.Failures)
	}
	if len(g.commits) != 0 || len(g.pushes) != 0 || len(pr.calls) != 0 {
		t.Fatalf("expected no commit/push/PR for up-to-date package")
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Fatalf("output should say up to date, got:\n%s", out.String())
	}
}

func TestRun_ManifestFailureBlocksPR(t *testing.T) {
	g := &mockGit{hasChanges: true, changes: []git.Change{{Path: "cat/foo/foo.ebuild", Status: git.Modified}}}
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
	g := &mockGit{hasChanges: true, changes: []git.Change{{Path: "cat/foo/foo.ebuild", Status: git.Modified}}}
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

func TestRun_FilterExclusionInteraction(t *testing.T) {
	g := &mockGit{}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		skipped: map[string]*source.SkippedError{
			"cat/excluded": {Package: "cat/excluded", Reason: "package is excluded: cat/excluded"},
		},
	}, &mockDirSyncer{}, &mockCommandRunner{})
	cfg.Filter = []string{"cat/excluded"}

	pkgs := []discovery.Package{{ID: "cat/excluded", Category: "cat", Name: "excluded", Path: "/repo/cat/excluded"}}
	var out strings.Builder
	summary, err := Run(context.Background(), cfg, pkgs, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.Excluded) != 1 || summary.Excluded[0] != "cat/excluded" {
		t.Fatalf("expected excluded package, got %v", summary.Excluded)
	}
	if len(summary.Created) != 0 || len(summary.MissingSource) != 0 {
		t.Fatalf("expected no PR or missing-source for excluded package")
	}
	if len(g.branches) != 0 {
		t.Fatalf("excluded package should not create a branch")
	}
}

func TestRun_AggregateFailureContinues(t *testing.T) {
	changes := []git.Change{{Path: "cat/bar/bar.ebuild", Status: git.Modified}}
	g := &mockGit{hasChanges: true, changes: changes}
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

func TestRun_AlreadyCoveredSummary(t *testing.T) {
	g := &mockGit{remoteBranches: map[string]bool{"update/cat-foo/abc123": true}}
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

	if len(summary.AlreadyCovered) != 1 {
		t.Fatalf("expected already covered, got %v", summary.AlreadyCovered)
	}
	if !strings.Contains(out.String(), "Already covered by existing update branches") {
		t.Fatalf("summary should report already covered, got:\n%s", out.String())
	}
}

func TestRun_CleanupFailureSurfaces(t *testing.T) {
	g := &mockGit{
		hasChanges:     true,
		changes:        []git.Change{{Path: "cat/foo/foo.ebuild", Status: git.Modified}},
		resetFailAfter: 1,
	}
	pr := &mockPRClient{url: "https://github.com/owner/repo/pull/42"}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "deadbeef1234", Dir: "/src/cat/foo"},
		},
	}, ensureDirSyncer{}, &mockCommandRunner{touchManifest: true})
	cfg.RootDir = t.TempDir()

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: filepath.Join(cfg.RootDir, "cat/foo")}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error when cleanup fails")
	}
	if len(summary.Created) != 1 {
		t.Fatalf("expected PR to be recorded before cleanup failure, got %v", summary.Created)
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Phase != "cleanup" {
		t.Fatalf("expected cleanup failure, got %v", summary.Failures)
	}
}

func TestRun_CleanupFailureStopsRun(t *testing.T) {
	g := &mockGit{
		hasChanges:     true,
		changes:        []git.Change{{Path: "cat/foo/foo.ebuild", Status: git.Modified}},
		resetFailAfter: 1,
	}
	pr := &mockPRClient{url: "https://github.com/owner/repo/pull/42"}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "deadbeef1234", Dir: "/src/cat/foo"},
			"cat/bar": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "barsha123456", Dir: "/src/cat/bar"},
		},
	}, ensureDirSyncer{}, &mockCommandRunner{touchManifest: true})
	cfg.RootDir = t.TempDir()

	pkgs := []discovery.Package{
		{ID: "cat/foo", Category: "cat", Name: "foo", Path: filepath.Join(cfg.RootDir, "cat/foo")},
		{ID: "cat/bar", Category: "cat", Name: "bar", Path: filepath.Join(cfg.RootDir, "cat/bar")},
	}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error when cleanup fails")
	}
	// Packages are processed in lexicographic order, so cat/bar is first.
	if len(summary.Created) != 1 || summary.Created[0].Package != "cat/bar" {
		t.Fatalf("expected PR for cat/bar only, got %v", summary.Created)
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Phase != "cleanup" || summary.Failures[0].Package != "cat/bar" {
		t.Fatalf("expected cleanup failure for cat/bar, got %v", summary.Failures)
	}
}

func TestRun_CleanupFailureAfterGateFailure(t *testing.T) {
	g := &mockGit{
		hasChanges:     true,
		changes:        []git.Change{{Path: "cat/foo/foo.ebuild", Status: git.Modified}},
		resetFailAfter: 1,
	}
	pr := &mockPRClient{}
	cfg := testConfig(g, pr, &mockSourceResolver{
		sources: map[string]source.ResolvedSource{
			"cat/foo": {Name: "guru", URL: "https://guru", Ref: "master", SHA: "deadbeef1234", Dir: "/src/cat/foo"},
		},
	}, ensureDirSyncer{}, &mockCommandRunner{manifestErr: errors.New("distfile fetch failed")})
	cfg.RootDir = t.TempDir()

	pkgs := []discovery.Package{{ID: "cat/foo", Category: "cat", Name: "foo", Path: filepath.Join(cfg.RootDir, "cat/foo")}}
	summary, err := Run(context.Background(), cfg, pkgs, io.Discard)
	if err == nil {
		t.Fatal("expected aggregate error when cleanup fails")
	}
	if len(summary.Failures) != 2 {
		t.Fatalf("expected manifest and cleanup failures, got %v", summary.Failures)
	}
	phases := map[string]bool{}
	for _, f := range summary.Failures {
		phases[f.Phase] = true
	}
	if !phases["manifest"] || !phases["cleanup"] {
		t.Fatalf("expected manifest and cleanup phases, got %v", summary.Failures)
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

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("git", "init", "--quiet")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README")
	run("git", "commit", "-m", "initial", "--quiet")
	run("git", "branch", "-m", "main")
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func TestRun_CleanupIsolation(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp)

	// Two packages on main.
	writeFiles(t, tmp, map[string]string{
		"cat/foo/foo.ebuild": "old foo\n",
		"cat/bar/bar.ebuild": "old bar\n",
		"cat/bar/only-local": "keep?\n",
	})
	runGit(t, tmp, "add", ".")
	runGit(t, tmp, "commit", "-m", "add packages", "--quiet")

	// Provide an origin remote so RemoteBranchExists can query it without
	// contacting the network. The bare repo starts with no branches.
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "--quiet")
	runGit(t, tmp, "remote", "add", "origin", origin)

	// Source directories with updated content.
	fooSrc := t.TempDir()
	writeFiles(t, fooSrc, map[string]string{"foo.ebuild": "new foo\n"})
	barSrc := t.TempDir()
	writeFiles(t, barSrc, map[string]string{"bar.ebuild": "new bar\n"})

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
		if !contains(files, wantEbuild) {
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
