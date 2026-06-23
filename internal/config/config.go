package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// packageIDRe matches Gentoo category/package identifiers used by the updater.
// It intentionally allows the characters Gentoo permits without enforcing
// leading-letter rules, because this config validator should stay small and
// reject obvious mistakes rather than duplicate Portage naming policy.
var packageIDRe = regexp.MustCompile(`^[a-z0-9+_.-]+/[a-zA-Z0-9+_.-]+$`)

// ErrPackageExcluded is returned by ResolveSource when the package is excluded
// from automatic updates.
var ErrPackageExcluded = errors.New("package is excluded")

// ErrPackageNotFound is returned by ResolveSource when the package path is not
// present in any configured source overlay.
var ErrPackageNotFound = errors.New("package not found in any configured source")

// Source describes one upstream overlay that can supply package updates.
type Source struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Ref  string `json:"ref"`
}

// Override holds per-package configuration that changes the default source
// selection or excludes the package from automatic updates.
//
// Pointer fields distinguish "omitted" from "explicitly set to zero value".
// An explicit empty string for Source or Ref is invalid and rejected during
// validation; an explicit false for Exclude is valid and means "do not exclude
// even if the package appears in Exclusions".
type Override struct {
	Exclude *bool   `json:"exclude,omitempty"`
	Source  *string `json:"source,omitempty"`
	Ref     *string `json:"ref,omitempty"`
}

// Config is the root updater configuration.
type Config struct {
	Sources      []Source            `json:"sources"`
	Exclusions   []string            `json:"exclusions"`
	BranchPrefix string              `json:"branchPrefix"`
	Overrides    map[string]Override `json:"overrides"`
}

// Load reads and validates the configuration at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()
	return LoadReader(f)
}

// LoadReader decodes and validates JSON configuration from r.
func LoadReader(r io.Reader) (*Config, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	// Reject trailing non-whitespace content after the first JSON value.
	// json.Decoder stops at the end of the first value, so a second decode
	// attempt fails only when there is additional data.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode config: trailing data after config object")
		}
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks that the configuration is internally consistent and usable.
func (c *Config) Validate() error {
	if len(c.Sources) == 0 {
		return fmt.Errorf("at least one source overlay is required")
	}
	seen := make(map[string]struct{}, len(c.Sources))
	for i, s := range c.Sources {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("source %d: name is required", i)
		}
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("source %q: url is required", s.Name)
		}
		if strings.TrimSpace(s.Ref) == "" {
			return fmt.Errorf("source %q: ref is required", s.Name)
		}
		if _, ok := seen[s.Name]; ok {
			return fmt.Errorf("duplicate source name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
	}

	if strings.TrimSpace(c.BranchPrefix) == "" {
		return fmt.Errorf("branchPrefix is required")
	}

	for _, pkg := range c.Exclusions {
		if !packageIDRe.MatchString(pkg) {
			return fmt.Errorf("invalid exclusion package id %q", pkg)
		}
	}

	for pkg, o := range c.Overrides {
		if !packageIDRe.MatchString(pkg) {
			return fmt.Errorf("invalid override package id %q", pkg)
		}
		if o.Source != nil && strings.TrimSpace(*o.Source) == "" {
			return fmt.Errorf("override for %q: source must not be empty", pkg)
		}
		if o.Ref != nil && strings.TrimSpace(*o.Ref) == "" {
			return fmt.Errorf("override for %q: ref must not be empty", pkg)
		}
		if o.Source != nil {
			if _, ok := seen[*o.Source]; !ok {
				return fmt.Errorf("override for %q: unknown source %q", pkg, *o.Source)
			}
		}
	}

	return nil
}

// IsExcluded reports whether pkg should be skipped by the updater. A per-package
// override with exclude=true wins over the global exclusions list; an override
// with exclude=false wins over a global exclusion for the same package.
func (c *Config) IsExcluded(pkg string) bool {
	if o, ok := c.Overrides[pkg]; ok && o.Exclude != nil {
		return *o.Exclude
	}
	for _, e := range c.Exclusions {
		if e == pkg {
			return true
		}
	}
	return false
}

// OverrideFor returns the override for pkg, if any.
func (c *Config) OverrideFor(pkg string) (Override, bool) {
	o, ok := c.Overrides[pkg]
	return o, ok
}

// SourceByName returns the configured source with the given name.
func (c *Config) SourceByName(name string) (Source, bool) {
	for _, s := range c.Sources {
		if s.Name == name {
			return s, true
		}
	}
	return Source{}, false
}

// ResolveSource selects the source and ref to use for pkg using the configured
// priority order and per-package overrides. The existsIn callback tells the
// resolver which source overlays actually contain the package path; sources are
// tried in order and the first one that exists wins when no source override is
// present. A callback error means the source overlay could not be prepared or
// probed, and the error is propagated so callers can treat it as a real failure
// rather than package absence.
//
// Override semantics:
//   - exclude=true skips the package.
//   - source override restricts lookup to that configured source.
//   - ref override applies only to the selected/overridden source.
//   - source override without a ref uses that source’s default configured ref.
//   - ref override without a source applies to the first source selected by
//     normal priority order.
func (c *Config) ResolveSource(pkg string, existsIn func(name string) (bool, error)) (Source, string, error) {
	if c.IsExcluded(pkg) {
		return Source{}, "", fmt.Errorf("%w: %s", ErrPackageExcluded, pkg)
	}

	o, hasOverride := c.Overrides[pkg]

	// Determine the effective source.
	var source Source
	var found bool
	if hasOverride && o.Source != nil {
		source, found = c.SourceByName(*o.Source)
		if !found {
			return Source{}, "", fmt.Errorf("override for %q references unknown source %q", pkg, *o.Source)
		}
	} else {
		for _, s := range c.Sources {
			ok, err := existsIn(s.Name)
			if err != nil {
				return Source{}, "", err
			}
			if ok {
				source = s
				found = true
				break
			}
		}
		if !found {
			return Source{}, "", fmt.Errorf("%w: %s", ErrPackageNotFound, pkg)
		}
	}

	// Determine the effective ref.
	ref := source.Ref
	if hasOverride && o.Ref != nil {
		ref = *o.Ref
	}

	return source, ref, nil
}
