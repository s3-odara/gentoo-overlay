package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		root, base, help, err := parseArgs(nil, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if root != "." {
			t.Fatalf("root: got %q, want %q", root, ".")
		}
		if base != "" {
			t.Fatalf("base branch flag: got %q, want empty", base)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		root, base, help, err := parseArgs([]string{
			"-root", "/tmp/overlay",
			"-base-branch", "dev",
		}, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if help {
			t.Fatal("unexpected help request")
		}
		if root != "/tmp/overlay" || base != "dev" {
			t.Fatalf("unexpected flags: root=%q base=%q", root, base)
		}
	})

	t.Run("rejects positional arguments", func(t *testing.T) {
		_, _, _, err := parseArgs([]string{"extra"}, io.Discard)
		if err == nil {
			t.Fatal("expected error for positional argument")
		}
		if !strings.Contains(err.Error(), "positional argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("help is not an error", func(t *testing.T) {
		var buf strings.Builder
		root, base, help, err := parseArgs([]string{"-help"}, &buf)
		if err != nil {
			t.Fatalf("parseArgs -help: %v", err)
		}
		if !help {
			t.Fatal("expected help request")
		}
		// The default values are returned so run can exit cleanly after printing
		// usage, but the caller is expected to stop processing on help.
		if root != "." || base != "" {
			t.Fatalf("unexpected flag values after help: %q %q", root, base)
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
