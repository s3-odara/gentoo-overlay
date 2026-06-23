package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/git"
	"github.com/s3-odara/gentoo-overlay/internal/github"
	"github.com/s3-odara/gentoo-overlay/internal/overlay"
	"github.com/s3-odara/gentoo-overlay/internal/source"
	"github.com/s3-odara/gentoo-overlay/internal/updater"
)

func main() {
	if err := run(context.Background(), os.Stdout, os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer, args []string, getenv func(string) string) error {
	rootDir, baseBranchFlag, help, err := parseArgs(args, out)
	if err != nil {
		return err
	}
	if help {
		return nil
	}

	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return fmt.Errorf("resolve root directory %q: %w", rootDir, err)
	}

	token := getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}

	owner, repo, err := splitRepository(getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}

	baseBranch := resolveBaseBranch(baseBranchFlag, getenv)

	pkgs, err := discovery.DiscoverPackages(rootAbs)
	if err != nil {
		return err
	}

	srcBase, err := os.MkdirTemp("", "gentoo-overlay-sources-*")
	if err != nil {
		return fmt.Errorf("create temporary source directory: %w", err)
	}
	defer os.RemoveAll(srcBase)

	srcMgr := source.NewManager(&source.GitCloner{}, srcBase)
	if err := srcMgr.Prepare(ctx); err != nil {
		return err
	}

	runCfg := updater.RunConfig{
		SourceResolver: srcMgr,
		Git:            &git.ExecDriver{},
		PRClient:       github.NewClient(token, nil),
		DirSyncer:      fsSyncer{},
		CommandRunner:  &updater.ExecCommandRunner{},
		RootDir:        rootAbs,
		Owner:          owner,
		Repo:           repo,
		BaseBranch:     baseBranch,
		BranchPrefix:   "update",
	}

	_, err = updater.Run(ctx, runCfg, pkgs, out)
	return err
}

// parseArgs parses CLI flags and returns the resolved values. The returned help
// flag is true when the user requested usage; callers should stop processing.
func parseArgs(args []string, out io.Writer) (rootDir, baseBranch string, help bool, err error) {
	rootDir = "."

	flags := flag.NewFlagSet("gentoo-overlay-updater", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&rootDir, "root", rootDir, "overlay repository root")
	flags.StringVar(&baseBranch, "base-branch", "", "base branch for update PRs (defaults to GITHUB_REF_NAME or main)")

	if err = flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return rootDir, baseBranch, true, nil
		}
		return "", "", false, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() > 0 {
		return "", "", false, fmt.Errorf("unexpected positional argument(s): %v", flags.Args())
	}
	return rootDir, baseBranch, false, nil
}

func resolveBaseBranch(flagValue string, getenv func(string) string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := getenv("GITHUB_REF_NAME"); v != "" {
		return v
	}
	return "main"
}

func splitRepository(value string) (string, string, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be set as owner/repo")
	}
	return parts[0], parts[1], nil
}

// fsSyncer adapts overlay.SyncRepo to updater.DirSyncer.
type fsSyncer struct{}

func (fsSyncer) SyncRepo(srcDir, dstDir string) error {
	return overlay.SyncRepo(srcDir, dstDir)
}
