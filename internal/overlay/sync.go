package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SyncRepo replaces the contents of dstDir with the contents of srcDir. It is
// intended for full package-directory synchronization: srcDir is the upstream
// package directory (for example the result of source.Manager.Resolve().Dir),
// and dstDir is the local package directory.
//
// The destination is removed and recreated, so files present locally but absent
// from the upstream package directory are deleted. Symlinks are preserved.
// This does not require srcDir to be a git repository; it performs a plain
// filesystem copy, which matches the contract expected by the updater when it
// resolves individual category/package paths inside a cloned source overlay.
func SyncRepo(srcDir, dstDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("stat source directory %s: %w", srcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s is not a directory", srcDir)
	}

	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", dstDir, err)
	}
	if err := os.RemoveAll(dstDir); err != nil {
		return fmt.Errorf("remove destination %s: %w", dstDir, err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create destination %s: %w", dstDir, err)
	}

	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dstDir, rel)

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

		mode, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, mode.Mode())
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
