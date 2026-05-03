package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// SecurityConfig is the apib-to-oas security sidecar. It is consumed by
// LoadSecurityConfig and applied to the in-memory OAS document just before
// marshalling. The shape mirrors the OAS spec where it can:
//
//   - SecuritySchemes is pasted verbatim into components.securitySchemes.
//   - DefaultSecurity becomes the document-level `security` array.
//   - Overrides scope a different requirement to specific operations,
//     matched by tag, raw URL path, or operationId. Empty `[]` means "no
//     security required", which lets callers expose anonymous endpoints
//     even when DefaultSecurity is set.
//
// Currently the loader accepts JSON only - keep parsing dependency-free.
// Authors can paste OAS-shaped securitySchemes blocks directly because
// SecurityScheme tags are JSON-compatible with the OAS field names.
type SecurityConfig struct {
	SecuritySchemes map[string]*oas.SecurityScheme `json:"securitySchemes,omitempty"`
	DefaultSecurity []oas.SecurityRequirement      `json:"defaultSecurity,omitempty"`
	Overrides       SecurityOverrides              `json:"overrides,omitempty"`
}

// SecurityOverrides scope per-operation security requirements. Precedence
// (most specific wins): ByOperationID > ByPath > ByTag > DefaultSecurity.
type SecurityOverrides struct {
	ByTag         map[string][]oas.SecurityRequirement `json:"byTag,omitempty"`
	ByPath        map[string][]oas.SecurityRequirement `json:"byPath,omitempty"`
	ByOperationID map[string][]oas.SecurityRequirement `json:"byOperationId,omitempty"`
}

// LoadSecurityConfig reads and decodes a JSON security sidecar from disk.
// Returns (nil, nil) when path is empty so callers can call this
// unconditionally.
func LoadSecurityConfig(path string) (*SecurityConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read security config: %w", err)
	}
	var cfg SecurityConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse security config: %w", err)
	}
	return &cfg, nil
}

// applySecurity merges cfg into doc: schemes go to components, defaults
// to doc.Security, overrides are pushed onto matching operations.
func applySecurity(doc *oas.Document, cfg *SecurityConfig) {
	if cfg == nil {
		return
	}
	if len(cfg.SecuritySchemes) > 0 {
		if doc.Components == nil {
			doc.Components = &oas.Components{}
		}
		if doc.Components.SecuritySchemes == nil {
			doc.Components.SecuritySchemes = map[string]*oas.SecurityScheme{}
		}
		for name, scheme := range cfg.SecuritySchemes {
			doc.Components.SecuritySchemes[name] = scheme
		}
	}
	if len(cfg.DefaultSecurity) > 0 {
		doc.Security = cfg.DefaultSecurity
	}
	// Per-operation overrides. Walk every operation once and pick the
	// most specific match. We resolve operationId first because it's the
	// finest grain, then path, then tag.
	for path, pi := range doc.Paths {
		for _, op := range pathOperations(pi) {
			if req, ok := lookupSecurityOverride(cfg.Overrides, path, op); ok {
				op.Security = req
			}
		}
	}
}

func lookupSecurityOverride(o SecurityOverrides, path string, op *oas.Operation) ([]oas.SecurityRequirement, bool) {
	if op.OperationID != "" {
		if req, ok := o.ByOperationID[op.OperationID]; ok {
			return req, true
		}
	}
	if req, ok := o.ByPath[path]; ok {
		return req, true
	}
	for _, t := range op.Tags {
		if req, ok := o.ByTag[t]; ok {
			return req, true
		}
	}
	return nil, false
}

// checkUndeclaredSecuritySchemes scans every security requirement on the
// document and on each operation, and emits Blueprint+ W007 for any
// referenced scheme name that isn't present in
// components.securitySchemes. Reports each missing name once, in
// alphabetical order so diagnostics are stable.
func checkUndeclaredSecuritySchemes(doc *oas.Document, diag *Diagnostics) {
	if diag == nil {
		return
	}
	declared := map[string]bool{}
	if doc.Components != nil {
		for name := range doc.Components.SecuritySchemes {
			declared[name] = true
		}
	}
	missing := map[string]bool{}
	collect := func(reqs []oas.SecurityRequirement) {
		for _, r := range reqs {
			for name := range r {
				if !declared[name] {
					missing[name] = true
				}
			}
		}
	}
	collect(doc.Security)
	for _, pi := range doc.Paths {
		for _, op := range pathOperations(pi) {
			collect(op.Security)
		}
	}
	for _, pi := range doc.Webhooks {
		for _, op := range pathOperations(pi) {
			collect(op.Security)
		}
	}
	if len(missing) == 0 {
		return
	}
	names := make([]string, 0, len(missing))
	for n := range missing {
		names = append(names, n)
	}
	sortStringsConvert(names)
	for _, n := range names {
		diag.Warn(CodeUndefinedScheme,
			"security scheme '"+n+"' is referenced but not declared in components.securitySchemes (declare via `## SecuritySchemes (object)` under `# Data Structures` or pass --security-config)")
	}
}
