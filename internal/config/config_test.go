package config

import (
	"strings"
	"testing"
)

func TestLoad_ValidDefaultConfig(t *testing.T) {
	cfg, err := Load("../../overlay-update-config.json")
	if err != nil {
		t.Fatalf("loading default config: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(cfg.Sources))
	}
	if cfg.Sources[0].Name != "guru" {
		t.Fatalf("expected first source guru, got %q", cfg.Sources[0].Name)
	}
	if cfg.Sources[1].Name != "gentoo-zh" {
		t.Fatalf("expected second source gentoo-zh, got %q", cfg.Sources[1].Name)
	}
	if cfg.BranchPrefix != "update" {
		t.Fatalf("expected branchPrefix update, got %q", cfg.BranchPrefix)
	}
	if !cfg.IsExcluded("virtual/notification-daemon") {
		t.Fatal("expected virtual/notification-daemon to be excluded")
	}
	if cfg.IsExcluded("gui-apps/fuzzel") {
		t.Fatal("did not expect gui-apps/fuzzel to be excluded")
	}
}

func TestLoadReader_MalformedJSON(t *testing.T) {
	_, err := LoadReader(strings.NewReader(`{not json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "decode config") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLoadReader_UnknownFields(t *testing.T) {
	input := `{
		"sources": [{"name": "guru", "url": "https://example.com/guru.git", "ref": "master"}],
		"branchPrefix": "update",
		"unknownField": true
	}`
	_, err := LoadReader(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "decode config") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLoadReader_TrailingData(t *testing.T) {
	base := `{
		"sources": [{"name": "guru", "url": "https://example.com/guru.git", "ref": "master"}],
		"branchPrefix": "update"
	}`
	cases := []string{
		base + ` {"extra": true}`,
		base + `trailing garbage`,
	}
	for _, input := range cases {
		_, err := LoadReader(strings.NewReader(input))
		if err == nil {
			t.Fatalf("expected error for trailing data in %q", input)
		}
		if !strings.Contains(err.Error(), "decode config") {
			t.Fatalf("expected decode error, got %v", err)
		}
	}
}

func TestValidate_DuplicateSourceNames(t *testing.T) {
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
			{Name: "guru", URL: "https://b.git", Ref: "main"},
		},
		BranchPrefix: "update",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for duplicate source names")
	} else if !strings.Contains(err.Error(), "duplicate source name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_MissingSourceName(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing source name")
	}
}

func TestValidate_MissingSourceURL(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "", Ref: "master"}},
		BranchPrefix: "update",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing source url")
	}
}

func TestValidate_MissingSourceRef(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: ""}},
		BranchPrefix: "update",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing source ref")
	}
}

func TestValidate_MissingBranchPrefix(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing branchPrefix")
	}
}

func TestValidate_InvalidExclusionPackageID(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		Exclusions:   []string{"not-a-package"},
		BranchPrefix: "update",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid exclusion package id")
	} else if !strings.Contains(err.Error(), "invalid exclusion") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_InvalidOverridePackageID(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"bad": {}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid override package id")
	} else if !strings.Contains(err.Error(), "invalid override") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_EmptyOverrideSource(t *testing.T) {
	empty := ""
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Source: &empty}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty override source")
	} else if !strings.Contains(err.Error(), "source must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_EmptyOverrideRef(t *testing.T) {
	empty := ""
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Ref: &empty}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty override ref")
	} else if !strings.Contains(err.Error(), "ref must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_UnknownOverrideSource(t *testing.T) {
	name := "missing"
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Source: &name}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown override source")
	} else if !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsExcluded_GlobalExclusion(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		Exclusions:   []string{"virtual/notification-daemon"},
		BranchPrefix: "update",
	}
	if !cfg.IsExcluded("virtual/notification-daemon") {
		t.Fatal("expected package to be excluded")
	}
}

func TestIsExcluded_OverrideExcludeWins(t *testing.T) {
	excluded := true
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Exclude: &excluded}},
	}
	if !cfg.IsExcluded("gui-apps/fuzzel") {
		t.Fatal("expected override exclude=true to win")
	}
}

func TestIsExcluded_OverrideIncludeWinsOverGlobalExclusion(t *testing.T) {
	included := false
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		Exclusions:   []string{"virtual/notification-daemon"},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"virtual/notification-daemon": {Exclude: &included}},
	}
	if cfg.IsExcluded("virtual/notification-daemon") {
		t.Fatal("expected override exclude=false to win over global exclusion")
	}
}

func TestResolveSource_PriorityOrder(t *testing.T) {
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
			{Name: "gentoo-zh", URL: "https://b.git", Ref: "main"},
		},
		BranchPrefix: "update",
	}
	src, ref, err := cfg.ResolveSource("gui-apps/fuzzel", func(name string) (bool, error) {
		return name == "gentoo-zh", nil // only gentoo-zh has it
	})
	if err != nil {
		t.Fatalf("ResolveSource failed: %v", err)
	}
	if src.Name != "gentoo-zh" {
		t.Fatalf("expected gentoo-zh, got %q", src.Name)
	}
	if ref != "main" {
		t.Fatalf("expected ref main, got %q", ref)
	}
}

func TestResolveSource_GuruPriorityWins(t *testing.T) {
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
			{Name: "gentoo-zh", URL: "https://b.git", Ref: "main"},
		},
		BranchPrefix: "update",
	}
	src, ref, err := cfg.ResolveSource("gui-apps/fuzzel", func(name string) (bool, error) {
		return true, nil // both have it
	})
	if err != nil {
		t.Fatalf("ResolveSource failed: %v", err)
	}
	if src.Name != "guru" {
		t.Fatalf("expected guru priority win, got %q", src.Name)
	}
	if ref != "master" {
		t.Fatalf("expected ref master, got %q", ref)
	}
}

func TestResolveSource_SourceOverride(t *testing.T) {
	name := "gentoo-zh"
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
			{Name: "gentoo-zh", URL: "https://b.git", Ref: "main"},
		},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Source: &name}},
	}
	src, ref, err := cfg.ResolveSource("gui-apps/fuzzel", func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ResolveSource failed: %v", err)
	}
	if src.Name != "gentoo-zh" {
		t.Fatalf("expected overridden source gentoo-zh, got %q", src.Name)
	}
	if ref != "main" {
		t.Fatalf("expected source default ref main, got %q", ref)
	}
}

func TestResolveSource_RefOverrideWithoutSource(t *testing.T) {
	ref := "stable"
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
		},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Ref: &ref}},
	}
	src, gotRef, err := cfg.ResolveSource("gui-apps/fuzzel", func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("ResolveSource failed: %v", err)
	}
	if src.Name != "guru" {
		t.Fatalf("expected guru, got %q", src.Name)
	}
	if gotRef != "stable" {
		t.Fatalf("expected overridden ref stable, got %q", gotRef)
	}
}

func TestResolveSource_SourceAndRefOverride(t *testing.T) {
	name := "gentoo-zh"
	ref := "stable"
	cfg := Config{
		Sources: []Source{
			{Name: "guru", URL: "https://a.git", Ref: "master"},
			{Name: "gentoo-zh", URL: "https://b.git", Ref: "main"},
		},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Source: &name, Ref: &ref}},
	}
	src, gotRef, err := cfg.ResolveSource("gui-apps/fuzzel", func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ResolveSource failed: %v", err)
	}
	if src.Name != "gentoo-zh" {
		t.Fatalf("expected gentoo-zh, got %q", src.Name)
	}
	if gotRef != "stable" {
		t.Fatalf("expected overridden ref stable, got %q", gotRef)
	}
}

func TestResolveSource_MissingFromAllSources(t *testing.T) {
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
	}
	_, _, err := cfg.ResolveSource("gui-apps/fuzzel", func(string) (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("expected error when package missing from all sources")
	}
}

func TestResolveSource_ExcludedPackage(t *testing.T) {
	excluded := true
	cfg := Config{
		Sources:      []Source{{Name: "guru", URL: "https://a.git", Ref: "master"}},
		BranchPrefix: "update",
		Overrides:    map[string]Override{"gui-apps/fuzzel": {Exclude: &excluded}},
	}
	_, _, err := cfg.ResolveSource("gui-apps/fuzzel", func(string) (bool, error) { return true, nil })
	if err == nil {
		t.Fatal("expected error for excluded package")
	}
}
