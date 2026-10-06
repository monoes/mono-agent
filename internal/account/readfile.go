package account

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// maxStoreFile is the most the store reads of session.json or refresh.enc: a
// session is a token and a few fields and a sealed refresh token is about a
// hundred bytes, so a file past this is not one the store wrote.
const maxStoreFile = 64 << 10

// The two ways readStoreFile refuses a file that is there.
var (
	errNotRegular = errors.New("is not a regular file")
	errTooLarge   = fmt.Errorf("is larger than %d KiB", maxStoreFile>>10)
)

// readStoreFile reads the file at path for the store. It opens the file without
// waiting (openForRead), asks the open file what it is and refuses anything but a
// regular file, and reads at most maxStoreFile bytes, asking for one more to tell
// a file of exactly that size from a larger one. A symbolic link is followed, as
// a linked folder is legitimate, but what it leads to must be a regular file.
// Without these checks a FIFO makes the read wait for a writer that never comes,
// in a daemon with the guard's reload lock held so that every Status waits behind
// it, and a link to a device that never ends is read until the process is out of
// memory. A refusal names the file and wraps errNotRegular or errTooLarge. Any
// other failure, a missing file included, is the open's, the stat's or the read's
// own error, as "account: reading <file>: ...".
func readStoreFile(path string) ([]byte, error) {
	name := filepath.Base(path)
	f, err := openForRead(path)
	if err != nil {
		return nil, fmt.Errorf("account: reading %s: %w", name, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("account: reading %s: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("account: %s %w", name, errNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStoreFile+1))
	if err != nil {
		return nil, fmt.Errorf("account: reading %s: %w", name, err)
	}
	if len(data) > maxStoreFile {
		return nil, fmt.Errorf("account: %s %w", name, errTooLarge)
	}
	return data, nil
}
