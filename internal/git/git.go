package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// ChangeStatus describes how git reports a path in a diff.
type ChangeStatus string

const (
	Added    ChangeStatus = "added"
	Modified ChangeStatus = "modified"
	Deleted  ChangeStatus = "deleted"
)

// Change records one changed path from git diff.
type Change struct {
	Path   string
	Status ChangeStatus
}

// ExecDriver shells out to git for production updater runs.
type ExecDriver struct{}

func (e *ExecDriver) CreateBranch(repoDir, branch, base string) error {
	out, err := exec.Command("git", "-C", repoDir, "checkout", "-b", branch, base).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create branch %q from %q: %w\n%s", branch, base, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *ExecDriver) Stage(repoDir string, paths ...string) error {
	args := []string{"-C", repoDir, "add", "-A", "--"}
	args = append(args, paths...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("stage changes: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *ExecDriver) HasStagedChanges(repoDir string, paths ...string) (bool, error) {
	args := []string{"-C", repoDir, "diff", "--cached", "--quiet", "--exit-code", "--"}
	args = append(args, paths...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("check staged diff: %w\n%s", err, strings.TrimSpace(string(out)))
}

func (e *ExecDriver) StagedChanges(repoDir string, paths ...string) ([]Change, error) {
	args := []string{"-C", repoDir, "diff", "--cached", "--name-status", "--"}
	args = append(args, paths...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list staged changes: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return parseNameStatus(string(out)), nil
}

func (e *ExecDriver) Commit(repoDir, message string) error {
	out, err := exec.Command("git", "-C", repoDir, "commit", "-m", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("commit changes: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *ExecDriver) Push(repoDir, remote, branch string) error {
	out, err := exec.Command("git", "-C", repoDir, "push", remote, branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("push branch %q to %q: %w\n%s\n(ensure the workflow job has permission 'contents: write' and the token is authorized to push to this repository)", branch, remote, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *ExecDriver) Checkout(repoDir, branch string) error {
	out, err := exec.Command("git", "-C", repoDir, "checkout", branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("checkout %q: %w\n%s", branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResetHard resets the working tree to HEAD and removes untracked files,
// leaving the repository in a clean state equivalent to a fresh checkout.
func (e *ExecDriver) ResetHard(repoDir string) error {
	out, err := exec.Command("git", "-C", repoDir, "reset", "--hard", "HEAD").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reset hard in %q: %w\n%s", repoDir, err, strings.TrimSpace(string(out)))
	}
	out, err = exec.Command("git", "-C", repoDir, "clean", "-fd").CombinedOutput()
	if err != nil {
		return fmt.Errorf("clean untracked files in %q: %w\n%s", repoDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResolveHead returns the short (12 character) SHA of the current HEAD of the
// repository at repoDir. It is used to fingerprint the upstream revision so the
// updater can name PR branches deterministically and avoid duplicate proposals.
func (e *ExecDriver) ResolveHead(repoDir string) (string, error) {
	return ResolveHead(repoDir)
}

// ResolveHead returns the short (12 character) SHA of the current HEAD of the
// repository at repoDir. The package-level helper lets other packages resolve
// HEAD without depending on the full ExecDriver.
func ResolveHead(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--short=12", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD in %q: %w\n%s", repoDir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteBranchExists reports whether the named branch exists on the given
// remote. git ls-remote returns exit code 2 when no matching refs are found,
// which is treated as "does not exist". Any other failure (e.g. the remote is
// unreachable) is returned as an error so the caller does not silently skip
// updates because of a configuration problem.
func (e *ExecDriver) RemoteBranchExists(repoDir, remote, branch string) (bool, error) {
	ref := fmt.Sprintf("refs/heads/%s", branch)
	out, err := exec.Command("git", "-C", repoDir, "ls-remote", "--exit-code", "--heads", remote, ref).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
		return false, nil
	}
	return false, fmt.Errorf("check remote branch %q on %q: %w\n%s", branch, remote, err, strings.TrimSpace(string(out)))
}

func parseNameStatus(output string) []Change {
	var changes []Change
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		status := statusFromGit(fields[0])
		if status == "" {
			continue
		}
		path := fields[len(fields)-1]
		changes = append(changes, Change{Path: path, Status: status})
	}
	return changes
}

func statusFromGit(status string) ChangeStatus {
	if status == "" {
		return ""
	}
	switch status[0] {
	case 'A':
		return Added
	case 'M', 'T', 'R', 'C':
		return Modified
	case 'D':
		return Deleted
	default:
		return ""
	}
}
