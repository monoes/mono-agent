//go:build devaccount

package account

import (
	"os"
	"strings"
)

// hostFromEnv is the monoes.me host of a devaccount build: MONOES_BASE_URL when
// set (a local server that signs with the development key), else HostURL.
func hostFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MONOES_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return HostURL
}
