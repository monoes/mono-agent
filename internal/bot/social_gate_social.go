//go:build social

package bot

// PlatformCompiledIn reports whether the given platform's bot adapter is
// compiled into this binary. In social builds every platform adapter is
// present.
func PlatformCompiledIn(platform string) bool {
	return true
}
