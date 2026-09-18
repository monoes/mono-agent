// Package fsconfine confines the file paths a workflow run's nodes read and
// write to one directory (caveat C-46 in the org × workflow unification
// plan).
//
// An org role that calls a granted automation runs it in mono-agent's
// daemon, outside monomind's per-role workdir confinement, so a role limited
// to its worktree could otherwise pass any path as automation input. The
// grant handler puts the role's workdir in the execution's trigger data as
// `org.workdir`; the engine turns that into a confined context
// (FromTriggerData), and every node that touches the filesystem passes its
// paths through Path before opening them.
//
// Without a root in the context, Path returns its argument unchanged: runs
// that no org role started behave exactly as before.
package fsconfine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideWorkdir is wrapped by every refusal of a path that resolves
// outside the confinement root.
var ErrOutsideWorkdir = errors.New("path escapes org workdir")

type ctxKey struct{}

// confinement is what a confined context carries. err is set when the
// trigger data asked for confinement but its root was unusable; every Path
// call then fails rather than falling back to no confinement.
type confinement struct {
	root string
	err  error
}

// WithRoot returns ctx confined to root. An empty root is recorded as an
// invalid confinement (fail closed), not as "no confinement": the caller
// asked for one.
func WithRoot(ctx context.Context, root string) context.Context {
	c := confinement{root: root}
	if root == "" {
		c.err = errors.New("org workdir is empty")
	}
	return context.WithValue(ctx, ctxKey{}, c)
}

func withInvalidRoot(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, ctxKey{}, confinement{err: errors.New(reason)})
}

// Root reports the confinement root of ctx, and whether ctx is confined at
// all (true also for an invalid confinement, whose root is "").
func Root(ctx context.Context) (string, bool) {
	c, ok := ctx.Value(ctxKey{}).(confinement)
	return c.root, ok
}

// TriggerKey is the trigger-data field that carries the root: the
// `workdir` entry of the `org` object the grant handler writes.
const TriggerKey = "workdir"

// FromTriggerData confines ctx when the execution's trigger data carries
// `org.workdir`. Absent: ctx is returned unchanged. Present but not a
// non-empty string: ctx is confined to an invalid root, so file nodes
// refuse every path.
func FromTriggerData(ctx context.Context, td map[string]interface{}) context.Context {
	org, ok := td["org"].(map[string]interface{})
	if !ok {
		return ctx
	}
	raw, present := org[TriggerKey]
	if !present {
		return ctx
	}
	s, ok := raw.(string)
	if !ok {
		return withInvalidRoot(ctx, fmt.Sprintf("org.workdir must be a string, got %T", raw))
	}
	return WithRoot(ctx, s)
}

// Path resolves p for a node's file access. Unconfined, p is returned
// unchanged. Confined, a relative p is taken relative to the root (as
// monomind does for the role's own file tools), `..` and symlinks are
// resolved, and the resolved path is returned only when it lies inside the
// root; otherwise the error wraps ErrOutsideWorkdir. Callers must open the
// returned path, not p, so what was checked is what is opened.
//
// A path whose tail does not exist yet (a file about to be written) is
// resolved through its deepest existing ancestor. A dangling symlink on the
// way is refused, because writing through it would create its target
// wherever it points. Residual risk: a process that swaps a directory for a
// symlink between this check and the open (TOCTOU) is not stopped; that
// needs the role to already have a shell, which is caveat C-2.
func Path(ctx context.Context, p string) (string, error) {
	c, ok := ctx.Value(ctxKey{}).(confinement)
	if !ok {
		return p, nil
	}
	if c.err != nil {
		return "", fmt.Errorf("%w: %v; refusing file access", ErrOutsideWorkdir, c.err)
	}
	if !filepath.IsAbs(c.root) {
		return "", fmt.Errorf("%w: org workdir %q is not an absolute path; refusing file access", ErrOutsideWorkdir, c.root)
	}
	root, err := filepath.EvalSymlinks(c.root)
	if err != nil {
		return "", fmt.Errorf("%w: org workdir %q is unavailable (%v); refusing file access", ErrOutsideWorkdir, c.root, err)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	resolved, err := resolveExisting(filepath.Clean(p))
	if err != nil {
		return "", fmt.Errorf("%w: %s (org workdir: %s): %v", ErrOutsideWorkdir, p, c.root, err)
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("%w: %s resolves to %s, outside the org workdir %s", ErrOutsideWorkdir, p, resolved, c.root)
	}
	return resolved, nil
}

// Paths applies Path to each element; one refusal refuses them all.
func Paths(ctx context.Context, ps []string) ([]string, error) {
	if ps == nil {
		return nil, nil
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		r, err := Path(ctx, p)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// resolveExisting evaluates symlinks in the longest existing prefix of the
// clean absolute path p and appends the missing tail verbatim.
func resolveExisting(p string) (string, error) {
	var tail []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if len(tail) > 0 {
				// The first missing component must be missing outright, not
				// a dangling symlink: EvalSymlinks fails on both.
				first := filepath.Join(real, tail[len(tail)-1])
				if fi, lerr := os.Lstat(first); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("%s is a symlink to a path that does not exist", first)
				}
			}
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// within reports whether p equals root or lies below it. Both are clean
// absolute paths.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
