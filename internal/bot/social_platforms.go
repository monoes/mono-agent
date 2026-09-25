package bot

import (
	"fmt"
	"strings"
)

// ErrNotCompiledIn is returned when a social-engagement platform is requested
// from a binary that was compiled without the "social" build tag.
func ErrNotCompiledIn(platform string) error {
	return fmt.Errorf("%s support not compiled in — this binary was built with -tags nosocial; rebuild without it", strings.ToLower(strings.TrimSpace(platform)))
}
