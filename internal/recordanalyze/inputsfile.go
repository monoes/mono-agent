package recordanalyze

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// maxInputsFile caps an inputs file; it holds a handful of values.
const maxInputsFile = 1 << 20

// ReadInputsFile reads a JSON object {name: value} of verify inputs. The
// file may hold secrets, so it must be a regular file, private to the user
// (no group/other permission bits) and owned by them. Errors never include
// the file's contents.
func ReadInputsFile(path string) (map[string]any, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inputs file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("inputs file: not a regular file")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("inputs file: mode %04o is readable by others; it must be 0600", perm)
	}
	if !ownedByCurrentUser(fi) {
		return nil, errors.New("inputs file: not owned by the current user")
	}
	if fi.Size() > maxInputsFile {
		return nil, errors.New("inputs file: too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("inputs file: %w", err)
	}
	var m map[string]any
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) || json.Unmarshal(b, &m) != nil || m == nil {
		return nil, errors.New("inputs file: not a JSON object of input name → value")
	}
	for k := range m {
		if !ValidInputName(k) {
			return nil, fmt.Errorf("inputs file: %q is not a valid input name", k)
		}
	}
	return m, nil
}
