package updater

import (
	"errors"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
	"github.com/s3-odara/gentoo-overlay/internal/git"
	"github.com/s3-odara/gentoo-overlay/internal/source"
)

func TestBuildPRBody_ContainsRequiredFields(t *testing.T) {
	pkg := discovery.Package{ID: "gui-apps/fuzzel", Category: "gui-apps", Name: "fuzzel"}
	src := source.ResolvedSource{
		Name: "guru",
		URL:  "https://anongit.gentoo.org/git/repo/proj/guru.git",
		Ref:  "master",
		SHA:  "deadbeef1234",
	}
	branch := "update/gui-apps-fuzzel/deadbeef1234"
	changes := []git.Change{
		{Path: "fuzzel-9999.ebuild", Status: git.Deleted},
		{Path: "fuzzel-42.ebuild", Status: git.Added},
		{Path: "metadata.xml", Status: git.Modified},
	}

	body := BuildPRBody(pkg, src, branch, changes)

	for _, want := range []string{
		"gui-apps/fuzzel",
		"guru",
		"https://anongit.gentoo.org/git/repo/proj/guru.git",
		"master",
		"deadbeef1234",
		"update/gui-apps-fuzzel/deadbeef1234",
		"deleted: `fuzzel-9999.ebuild`",
		"added: `fuzzel-42.ebuild`",
		"modified: `metadata.xml`",
		"fully replaces the local package directory",
		"Local-only files",
		"Manifest",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body missing %q:\n%s", want, body)
		}
	}
}

func TestBuildPRBody_NoChanges(t *testing.T) {
	pkg := discovery.Package{ID: "cat/pkg", Category: "cat", Name: "pkg"}
	src := source.ResolvedSource{Name: "guru", URL: "https://guru", Ref: "master", SHA: "abc"}
	body := BuildPRBody(pkg, src, "update/cat-pkg/abc", nil)
	if !strings.Contains(body, "No file changes detected") {
		t.Fatalf("expected no-changes note, got:\n%s", body)
	}
}

func TestPrintSummary_MarkdownSections(t *testing.T) {
	summary := &RunSummary{
		Created: []PRCreated{
			{Package: "cat/one", Branch: "update/cat-one/aaa", URL: "https://example.com/1", Source: "guru"},
		},
		UpToDate:       []string{"cat/two"},
		AlreadyCovered: []string{"cat/three"},
		MissingSource:  []string{"cat/four"},
		Excluded:       []string{"cat/five"},
		Failures:       []Failure{{Package: "cat/six", Phase: "manifest", Err: errors.New("boom")}},
	}

	var out strings.Builder
	PrintSummary(&out, summary)
	s := out.String()

	for _, want := range []string{
		"## Gentoo overlay update summary",
		"### Created pull requests (1)",
		"cat/one",
		"### Up to date (1)",
		"cat/two",
		"### Already covered by existing update branches (1)",
		"cat/three",
		"### Missing from source overlays (1)",
		"cat/four",
		"### Excluded (1)",
		"cat/five",
		"### Failures (1)",
		"cat/six",
		"manifest",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q:\n%s", want, s)
		}
	}
}

func TestPrintSummary_Empty(t *testing.T) {
	var out strings.Builder
	PrintSummary(&out, &RunSummary{})
	if !strings.Contains(out.String(), "No packages were processed") {
		t.Fatalf("expected empty summary note, got:\n%s", out.String())
	}
}
