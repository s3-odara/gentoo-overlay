package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s3-odara/gentoo-overlay/internal/updater"
)

func TestParseArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, root, base, help, err := parseArgs(nil, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if cfg != defaultConfig {
			t.Fatalf("config path: got %q, want %q", cfg, defaultConfig)
		}
		if root != "." {
			t.Fatalf("root: got %q, want %q", root, ".")
		}
		if base != "" {
			t.Fatalf("base branch flag: got %q, want empty", base)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg, root, base, help, err := parseArgs([]string{
			"-config", "custom.json",
			"-root", "/tmp/overlay",
			"-base-branch", "dev",
		}, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if cfg != "custom.json" || root != "/tmp/overlay" || base != "dev" {
			t.Fatalf("unexpected flags: config=%q root=%q base=%q", cfg, root, base)
		}
	})

	t.Run("rejects positional arguments", func(t *testing.T) {
		_, _, _, _, err := parseArgs([]string{"extra"}, io.Discard)
		if err == nil {
			t.Fatal("expected error for positional argument")
		}
		if !strings.Contains(err.Error(), "positional argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("help is not an error", func(t *testing.T) {
		var buf strings.Builder
		cfg, root, base, help, err := parseArgs([]string{"-help"}, &buf)
		if err != nil {
			t.Fatalf("parseArgs -help: %v", err)
		}
		if !help {
			t.Fatal("expected help request")
		}
		// The default values are returned so run can exit cleanly after printing
		// usage, but the caller is expected to stop processing on help.
		if cfg != defaultConfig || root != "." || base != "" {
			t.Fatalf("unexpected flag values after help: %q %q %q", cfg, root, base)
		}
		if !strings.Contains(buf.String(), "Usage") {
			t.Fatalf("help output missing usage: %q", buf.String())
		}
	})
}

func TestResolveBaseBranch(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		got := resolveBaseBranch("dev", func(string) string { return "main" })
		if got != "dev" {
			t.Fatalf("got %q, want dev", got)
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		got := resolveBaseBranch("", func(k string) string {
			if k == "GITHUB_REF_NAME" {
				return "ci-branch"
			}
			return ""
		})
		if got != "ci-branch" {
			t.Fatalf("got %q, want ci-branch", got)
		}
	})

	t.Run("default main", func(t *testing.T) {
		got := resolveBaseBranch("", func(string) string { return "" })
		if got != "main" {
			t.Fatalf("got %q, want main", got)
		}
	})
}

func TestRun_MissingToken(t *testing.T) {
	env := func(k string) string {
		if k == "GITHUB_REPOSITORY" {
			return "owner/repo"
		}
		return ""
	}
	err := run(context.Background(), io.Discard, nil, env)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_InvalidRepository(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "GITHUB_TOKEN":
			return "token"
		case "GITHUB_REPOSITORY":
			return "not-owner-slash-repo"
		}
		return ""
	}
	err := run(context.Background(), io.Discard, nil, env)
	if err == nil {
		t.Fatal("expected error for invalid repository")
	}
	if !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_Help(t *testing.T) {
	var buf strings.Builder
	err := run(context.Background(), &buf, []string{"-help"}, nil)
	if err != nil {
		t.Fatalf("run -help: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage") {
		t.Fatalf("help output missing usage: %q", buf.String())
	}
}

func TestWriteSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	summary := &updater.RunSummary{
		Created: []updater.PRCreated{
			{Package: "app-misc/lf", Branch: "update/app-misc-lf/abc123", URL: "https://example.com/pr/1", Source: "guru"},
		},
	}
	if err := writeSummary(summary, path); err != nil {
		t.Fatalf("writeSummary: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(data), "app-misc/lf") {
		t.Fatalf("summary missing package: %s", data)
	}
	if !strings.Contains(string(data), "Created pull requests") {
		t.Fatalf("summary missing header: %s", data)
	}
}
