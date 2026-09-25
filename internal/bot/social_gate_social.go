//go:build !nosocial

package bot

// PlatformCompiledIn reports whether the given platform's bot adapter is
// compiled into this binary. In default builds (without -tags nosocial) every platform adapter is
// present.
func PlatformCompiledIn(platform string) bool {
	return true
}
