package vault

import "context"

// ProfileIDInContext is ProfileIDFromContext without the "default"
// fallback: it reports whether ctx carries a profile at all, so a caller
// with its own fallback (the CLI's browser routing) can tell "no profile"
// from a profile whose id happens to be "default".
func ProfileIDInContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(profileCtxKey{}).(string)
	return v, ok && v != ""
}
