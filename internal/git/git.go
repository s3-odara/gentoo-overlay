package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// ExecDriver shells out to git for production updater runs.
type ExecDriver struct{}

// runGit executes git -C repoDir with args and wraps any failure with the
// command output. It is the single error-formatting site for the package.
func runGit(repoDir string, args ...string) error {
	full := append([]string{"-C", repoDir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *ExecDriver) CreateBranch(repoDir, branch, base string) error {
	return runGit(repoDir, "checkout", "-b", branch, base)
}

func (e *ExecDriver) Stage(repoDir string, paths ...string) error {
	return runGit(repoDir, append([]string{"add", "-A", "--"}, paths...)...)
}

func (e *ExecDriver) HasStagedChanges(repoDir string, paths ...string) (bool, error) {
	out, err := exec.Command("git", append([]string{"-C", repoDir, "diff", "--cached", "--quiet", "--exit-code", "--"}, paths...)...).CombinedOutput()
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("check staged diff: %w\n%s", err, strings.TrimSpace(string(out)))
}

func (e *ExecDriver) Commit(repoDir, message string) error {
	return runGit(repoDir, "commit", "-m", message)
}

func (e *ExecDriver) Push(repoDir, remote, branch string) error {
	if err := runGit(repoDir, "push", remote, branch); err != nil {
		return fmt.Errorf("%w\n(ensure the workflow job has permission 'contents: write' and the token is authorized to push to this repository)", err)
	}
	return nil
}

func (e *ExecDriver) Checkout(repoDir, branch string) error {
	return runGit(repoDir, "checkout", branch)
}

// ResetHard resets the working tree to HEAD and removes untracked files,
// leaving the repository in a clean state equivalent to a fresh checkout.
func (e *ExecDriver) ResetHard(repoDir string) error {
	if err := runGit(repoDir, "reset", "--hard", "HEAD"); err != nil {
		return err
	}
	return runGit(repoDir, "clean", "-fd")
}

// ResolveHead returns the short (12 character) SHA of the current HEAD of the
// repository at repoDir. It is used to fingerprint the upstream revision so the
// updater can name PR branches deterministically and avoid duplicate proposals.
func ResolveHead(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--short=12", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD in %q: %w\n%s", repoDir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
