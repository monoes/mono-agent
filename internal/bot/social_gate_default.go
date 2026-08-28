//go:build !social

package bot

import "strings"

// socialTagPlatforms are the social-engagement platforms whose bot packages
// are excluded from default builds (they require -tags social).
var socialTagPlatforms = map[string]bool{
	"INSTAGRAM":   true,
	"LINKEDIN":    true,
	"TIKTOK":      true,
	"X":           true,
	"HACKERNEWS":  true,
	"PRODUCTHUNT": true,
}

// PlatformCompiledIn reports whether the given platform's bot adapter is
// compiled into this binary.
func PlatformCompiledIn(platform string) bool {
	return !socialTagPlatforms[strings.ToUpper(strings.TrimSpace(platform))]
}
