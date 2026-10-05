//go:build windows

package orgsign

func statfsType(string) string { return "" }

// platformUntrusted: os.FileInfo on Windows carries no file index or change
// time, so a file or folder swapped and put back can't be told apart.
func platformUntrusted() string {
	return "on Windows mono-agent can't tell a file swapped and put back from an untouched one — sign this org with `monomind org sign` in a terminal (or with monomind 2.22, whose review signs exactly what it showed)"
}
