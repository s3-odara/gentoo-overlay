package source

// Source describes one upstream overlay that can supply package updates.
type Source struct {
	Name string
	URL  string
	Ref  string
}

// hardcoded configuration, not configurable at runtime.
var (
	sources = []Source{
		{Name: "guru", URL: "https://anongit.gentoo.org/git/repo/proj/guru.git", Ref: "master"},
		{Name: "gentoo-zh", URL: "https://github.com/microcai/gentoo-zh.git", Ref: "master"},
	}
	exclusions = []string{"virtual/notification-daemon"}
)

const branchPrefix = "update"

// isExcluded reports whether pkg is excluded from automatic updates.
func isExcluded(pkg string) bool {
	for _, e := range exclusions {
		if e == pkg {
			return true
		}
	}
	return false
}
