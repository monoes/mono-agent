package action

// Upload confinement for package actions (security contract §8, C1). An
// upload hands local files to a web page, so a package — which may be
// imported from anyone — must not be able to name them itself. With a
// package attached, each file must be either:
//
//   - exactly the value the user gave an input declared with type "file"
//     or "path" (and not merely that input's declared default, which the
//     package controls), or
//   - inside ~/.monoagent/uploads/<automation id>/ or the run's fsconfine
//     root, after resolving symlinks.
//
// Anything else — ~/.ssh/id_rsa typed into the step, a template over a
// plain variable, a symlink out of the uploads directory — is refused
// before the page is touched.
//
// builtin and local packages (shipped with the binary, or written by the
// user on this machine) are exempt: their legacy string-form inputs carry
// no type (tiktok publish_post uploads "{{media}}"). Every other tier,
// including a package that does not report one, is confined.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/fsconfine"
)

// ErrUploadNotAllowed is wrapped by every package upload refusal.
var ErrUploadNotAllowed = errors.New("upload path not allowed")

// UploadsDir returns ~/.monoagent/uploads/<automationID>, the directory a
// package may upload from without an input naming the file.
func UploadsDir(automationID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".monoagent", "uploads", automationID), nil
}

// confineUploads checks the upload step's files (raw as named, resolved as
// returned by fsconfine) against the package rules. No package: nil.
func (ae *ActionExecutor) confineUploads(ctx context.Context, raw, resolved []string) error {
	if ae.pkg == nil || !untrustedTier(PackageTrust(ae.pkg)) {
		return nil
	}
	allowedValues := ae.fileInputValues()
	var roots []string
	if dir, err := UploadsDir(ae.pkg.ID()); err == nil {
		roots = append(roots, dir)
	}
	if root, confined := fsconfine.Root(ctx); confined && root != "" {
		roots = append(roots, root)
	}
	for i, p := range raw {
		if allowedValues[filepath.Clean(strings.TrimSpace(p))] {
			continue
		}
		target := p
		if i < len(resolved) {
			target = resolved[i]
		}
		if !insideAnyRoot(target, roots) {
			return fmt.Errorf("%w: %q is neither a file input's value nor inside %s", ErrUploadNotAllowed, p, strings.Join(roots, " or "))
		}
	}
	return nil
}

// fileInputValues returns the cleaned paths the user supplied to inputs
// declared with type file/path, excluding values equal to the declared
// default.
func (ae *ActionExecutor) fileInputValues() map[string]bool {
	out := map[string]bool{}
	if ae.actionDef == nil || ae.actionDef.Inputs == nil {
		return out
	}
	decls := append(append([]json.RawMessage{}, ae.actionDef.Inputs.Required...), ae.actionDef.Inputs.Optional...)
	for _, raw := range decls {
		var in struct {
			Name    string      `json:"name"`
			Type    string      `json:"type"`
			Default interface{} `json:"default"`
		}
		if json.Unmarshal(raw, &in) != nil || in.Name == "" {
			continue
		}
		if t := strings.ToLower(in.Type); t != "file" && t != "path" {
			continue
		}
		defaults := map[string]bool{}
		for _, d := range pathValues(in.Default) {
			defaults[d] = true
		}
		v, _ := ae.execCtx.GetVariable(in.Name)
		for _, p := range pathValues(v) {
			if !defaults[p] {
				out[p] = true
			}
		}
	}
	return out
}

// pathValues flattens an input value (string, comma list, or list) into
// cleaned paths.
func pathValues(v interface{}) []string {
	var parts []string
	switch t := v.(type) {
	case string:
		parts = strings.Split(t, ",")
	case []string:
		parts = t
	case []interface{}:
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// insideAnyRoot reports whether p, with symlinks resolved, lies inside one
// of roots (also resolved). A path that cannot be resolved is outside.
func insideAnyRoot(p string, roots []string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	for _, r := range roots {
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rr, real)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "." {
			return true
		}
	}
	return false
}
