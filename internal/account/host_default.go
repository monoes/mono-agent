//go:build !devaccount

package account

// hostFromEnv is the monoes.me host of a default build: always HostURL. No
// environment variable redirects the session (spec D24); MONOES_BASE_URL still
// redirects the library, which then has no session to send to that host.
func hostFromEnv() string { return HostURL }
