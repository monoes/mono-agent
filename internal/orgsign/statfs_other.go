//go:build !linux && !darwin && !windows

package orgsign

// statfsType is unknown here: the filesystem is not checked.
func statfsType(string) string { return "" }

func platformUntrusted() string { return "" }
