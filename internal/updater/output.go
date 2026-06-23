package updater

import (
	"fmt"
	"io"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/source"
)

// BuildPRBody returns a minimal markdown pull request body describing the update.
func BuildPRBody(pkg discovery.Package, src source.ResolvedSource) string {
	return fmt.Sprintf("Update `%s` from the `%s` overlay.\n\n- Source: %s\n- URL: %s\n- Ref: %s\n- Commit: %s\n",
		pkg.ID, src.Name, src.Name, src.URL, src.Ref, src.SHA)
}

// PrintSummary writes a minimal summary after the run.
func PrintSummary(out io.Writer, summary *RunSummary) {
	fmt.Fprintf(out, "Created %d pull request(s)\n", len(summary.Created))
	for _, pr := range summary.Created {
		fmt.Fprintf(out, "  %s from %s: %s (%s)\n", pr.Package, pr.Source, pr.URL, pr.Branch)
	}
	fmt.Fprintf(out, "%d failure(s)\n", len(summary.Failures))
	for _, f := range summary.Failures {
		fmt.Fprintf(out, "  %s (%s): %s\n", f.Package, f.Phase, f.Err)
	}
}
