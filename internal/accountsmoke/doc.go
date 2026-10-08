// Package accountsmoke is the real-binary smoke test of the monoes.me account gate: the CLI built
// with -tags devaccount, run as subprocesses against a fake monoes.me. It holds tests only, and
// they build only with the tag (go test -tags devaccount ./internal/accountsmoke/), so a plain
// go test ./... never builds the binary.
package accountsmoke
