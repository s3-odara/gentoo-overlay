package updater

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/git"
	"github.com/s3-odara/gentoo-overlay/internal/source"
)

// stripPackagePrefix removes the leading "category/package/" from git change
// paths so PR bodies list file names relative to the package directory.
func stripPackagePrefix(pkgPath string, changes []git.Change) []git.Change {
	prefix := filepath.ToSlash(pkgPath) + "/"
	stripped := make([]git.Change, 0, len(changes))
	for _, c := range changes {
		c.Path = strings.TrimPrefix(filepath.ToSlash(c.Path), prefix)
		stripped = append(stripped, c)
	}
	return stripped
}

// BuildPRBody returns a markdown pull request body describing the update.
func BuildPRBody(pkg discovery.Package, src source.ResolvedSource, branch string, changes []git.Change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Update `%s` from the `%s` overlay.\n\n", pkg.ID, src.Name)

	fmt.Fprintln(&b, "### Source")
	fmt.Fprintf(&b, "- Overlay: %s\n", src.Name)
	fmt.Fprintf(&b, "- URL: %s\n", src.URL)
	fmt.Fprintf(&b, "- Ref: %s\n", src.Ref)
	fmt.Fprintf(&b, "- Commit: %s\n", src.SHA)
	fmt.Fprintf(&b, "- Branch: %s\n", branch)

	fmt.Fprintln(&b, "\n### Changed files")
	if len(changes) == 0 {
		fmt.Fprintln(&b, "_No file changes detected._")
	} else {
		for _, c := range changes {
			switch c.Status {
			case git.Added:
				fmt.Fprintf(&b, "- added: `%s`\n", c.Path)
			case git.Modified:
				fmt.Fprintf(&b, "- modified: `%s`\n", c.Path)
			case git.Deleted:
				fmt.Fprintf(&b, "- deleted: `%s`\n", c.Path)
			default:
				fmt.Fprintf(&b, "- %s: `%s`\n", c.Status, c.Path)
			}
		}
	}

	fmt.Fprintln(&b, "\n### Review notes")
	fmt.Fprintln(&b, "- This update fully replaces the local package directory with the selected upstream package directory.")
	fmt.Fprintln(&b, "- Local-only files, patches, live ebuilds, retained revisions, or metadata entries that are not present upstream are removed in this branch.")
	fmt.Fprintln(&b, "- The `Manifest` was regenerated with `pkgdev manifest` rather than copied verbatim from the source overlay.")
	fmt.Fprintln(&b, "- Please inspect the diff and confirm the changes are trustworthy before merging.")
	return b.String()
}

// PrintSummary writes a markdown workflow summary from a run result.
func PrintSummary(out io.Writer, summary *RunSummary) {
	fmt.Fprintf(out, "## Gentoo overlay update summary\n\n")

	if len(summary.Created) > 0 {
		fmt.Fprintf(out, "### Created pull requests (%d)\n", len(summary.Created))
		for _, pr := range summary.Created {
			fmt.Fprintf(out, "- `%s` from %s: %s (%s)\n", pr.Package, pr.Source, pr.URL, pr.Branch)
		}
		fmt.Fprintln(out)
	}

	if len(summary.UpToDate) > 0 {
		fmt.Fprintf(out, "### Up to date (%d)\n", len(summary.UpToDate))
		for _, pkg := range summary.UpToDate {
			fmt.Fprintf(out, "- %s\n", pkg)
		}
		fmt.Fprintln(out)
	}

	if len(summary.AlreadyCovered) > 0 {
		fmt.Fprintf(out, "### Already covered by existing update branches (%d)\n", len(summary.AlreadyCovered))
		for _, pkg := range summary.AlreadyCovered {
			fmt.Fprintf(out, "- %s\n", pkg)
		}
		fmt.Fprintln(out)
	}

	if len(summary.MissingSource) > 0 {
		fmt.Fprintf(out, "### Missing from source overlays (%d)\n", len(summary.MissingSource))
		for _, pkg := range summary.MissingSource {
			fmt.Fprintf(out, "- %s\n", pkg)
		}
		fmt.Fprintln(out)
	}

	if len(summary.Excluded) > 0 {
		fmt.Fprintf(out, "### Excluded (%d)\n", len(summary.Excluded))
		for _, pkg := range summary.Excluded {
			fmt.Fprintf(out, "- %s\n", pkg)
		}
		fmt.Fprintln(out)
	}

	if len(summary.Failures) > 0 {
		fmt.Fprintf(out, "### Failures (%d)\n", len(summary.Failures))
		for _, f := range summary.Failures {
			fmt.Fprintf(out, "- `%s` (%s): %s\n", f.Package, f.Phase, f.Err)
		}
		fmt.Fprintln(out)
	}

	if len(summary.Created) == 0 && len(summary.UpToDate) == 0 &&
		len(summary.AlreadyCovered) == 0 && len(summary.MissingSource) == 0 &&
		len(summary.Excluded) == 0 && len(summary.Failures) == 0 {
		fmt.Fprintln(out, "No packages were processed.")
	}
}
