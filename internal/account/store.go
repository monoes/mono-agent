package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	sessionFile = "session.json"
	refreshFile = "refresh.enc"
	lockFile    = "session.lock"
)

// Store is the on-disk session. Nothing creates a file or a directory until a
// write: every read of a missing session answers "none". The mutating methods
// (Save, SaveRefresh, DeleteRefresh) are safe across processes only under Lock.
type Store interface {
	Load() (*Session, error)                             // nil, nil when there is no session.json
	Save(*Session) error                                 // atomic, 0600
	LoadRefresh() (string, error)                        // "", nil when none; ErrKeyringUnavailable when the key store cannot be opened
	SaveRefresh(token string) error                      // seals it; the directory is created here if needed
	DeleteRefresh() error                                // nil when there is none
	Lock(ctx context.Context) (unlock func(), err error) // exclusive, cross-process
	Mtime() (time.Time, error)                           // of session.json; zero time when absent
	Dir() string
}

// DefaultDir is ~/.monoagent/account, found through os.UserHomeDir. The path
// is the same whatever --db-path says: there is one session per OS user.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("account: finding the home directory: %w", err)
	}
	return filepath.Join(home, ".monoagent", "account"), nil
}

type fileStore struct {
	dir    string
	sealer Sealer
	err    error // the default directory could not be resolved: no session can exist, and none can be written
}

// OpenStore opens the session in dir (the default directory when dir is "")
// with s as the refresh-token sealer (the production keyring sealer when s is
// nil). It touches nothing on disk.
func OpenStore(dir string, s Sealer) Store {
	st := &fileStore{dir: dir, sealer: s}
	if dir == "" {
		st.dir, st.err = DefaultDir()
	}
	if s == nil {
		st.sealer = NewKeyringSealer()
	}
	return st
}

func (s *fileStore) Dir() string { return s.dir }

func (s *fileStore) path(name string) string { return filepath.Join(s.dir, name) }

func (s *fileStore) Load() (*Session, error) {
	if s.err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(s.path(sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("account: reading %s: %w", sessionFile, err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("account: %s is not valid: %w", sessionFile, err)
	}
	if sess.V != sessionVersion {
		return nil, fmt.Errorf("account: %s has version %d, this build reads version %d", sessionFile, sess.V, sessionVersion)
	}
	return &sess, nil
}

func (s *fileStore) Save(sess *Session) error {
	if s.err != nil {
		return s.err
	}
	if sess == nil {
		return errors.New("account: no session to save")
	}
	cp := *sess
	cp.V = sessionVersion
	data, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(sessionFile), append(data, '\n'))
}

func (s *fileStore) Mtime() (time.Time, error) {
	if s.err != nil {
		return time.Time{}, nil
	}
	fi, err := os.Stat(s.path(sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// LoadRefresh unseals the refresh token. A missing refresh.enc is "", nil and
// never reaches the key store, so reading a machine with no session cannot
// raise a keychain prompt.
func (s *fileStore) LoadRefresh() (string, error) {
	if s.err != nil {
		return "", nil
	}
	sealed, err := os.ReadFile(s.path(refreshFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("account: reading %s: %w", refreshFile, err)
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *fileStore) SaveRefresh(token string) error {
	if s.err != nil {
		return s.err
	}
	if token == "" {
		return errors.New("account: empty refresh token")
	}
	sealed, err := s.sealer.Seal([]byte(token))
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(refreshFile), sealed)
}

func (s *fileStore) DeleteRefresh() error {
	if s.err != nil {
		return nil
	}
	if err := os.Remove(s.path(refreshFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// errLockHeld is what tryLock returns while another holder has the lock.
var errLockHeld = errors.New("account: lock is held")

// Lock takes the exclusive cross-process lock on session.lock, waiting until
// ctx ends. The lock file and the directory are created here, so Lock is only
// for a caller that is about to write. unlock is safe to call twice. The OS
// drops the lock if the process dies, so a crash leaves nothing stale.
func (s *fileStore) Lock(ctx context.Context) (func(), error) {
	if s.err != nil {
		return nil, s.err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path(lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for delay := 5 * time.Millisecond; ; {
		err := tryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errLockHeld) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 50*time.Millisecond)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlock(f)
			f.Close()
		})
	}, nil
}

// writeFileAtomic writes data to path (mode 0600, directory 0700 created on
// the first write) through a temporary file in the same directory and a rename,
// so a reader sees the old file or the new one, never half of one.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has moved it
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameReplacing(tmp.Name(), path)
}

// renameReplacing renames over an existing file. On Windows the rename can
// fail while another process has the target open, so it is retried briefly: a
// lost write here could lose a rotated refresh token.
func renameReplacing(from, to string) error {
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 5
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}
