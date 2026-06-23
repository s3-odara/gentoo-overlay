package updater

import (
	"errors"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/discovery"
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

	body := BuildPRBody(pkg, src)

	for _, want := range []string{
		"gui-apps/fuzzel",
		"guru",
		"https://anongit.gentoo.org/git/repo/proj/guru.git",
		"master",
		"deadbeef1234",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body missing %q:\n%s", want, body)
		}
	}
}

func TestPrintSummary(t *testing.T) {
	summary := &RunSummary{
		Created: []PRCreated{
			{Package: "cat/one", Branch: source.BranchPrefix + "/cat-one/aaa", URL: "https://example.com/1", Source: "guru"},
		},
		Failures: []Failure{{Package: "cat/six", Phase: "manifest", Err: errors.New("boom")}},
	}

	var out strings.Builder
	PrintSummary(&out, summary)
	s := out.String()

	for _, want := range []string{
		"Created 1 pull request(s)",
		"cat/one",
		"1 failure(s)",
		"cat/six",
		"manifest",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q:\n%s", want, s)
		}
	}
}
