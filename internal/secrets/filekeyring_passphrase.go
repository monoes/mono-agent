package secrets

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/term"
)

// filePassphraseFileEnv names a file holding the file-keyring passphrase:
// the non-interactive source for hosts where nobody can type it (the desktop
// app, services, CI). It carries a PATH, never the passphrase itself — a
// passphrase in an environment variable or flag leaks through process
// listings (/proc/<pid>/environ) and shell history, which is why this
// package never accepts one there. The file must not be readable by group
// or others (chmod 600), the same rule ssh applies to private keys.
const filePassphraseFileEnv = "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE"

// filePassphraseHint is appended to every "no passphrase available" error so
// the operator learns the non-interactive option instead of a bare
// "empty passphrase".
const filePassphraseHint = "store the passphrase in a file only you can read (chmod 600) and set " +
	filePassphraseFileEnv + "=/path/to/that/file"

// stdinConsumed records that this process has already used stdin for its
// own input (a secret value, a JSON payload, a confirmation, an MCP
// session…). Once set, the file-keyring passphrase is never read from
// stdin: it would either get the tail of the data stream or, far more
// commonly, EOF — the "empty file-keyring passphrase" failure on the first
// vault write of `jev key set` / `secret add`.
var stdinConsumed atomic.Bool

// MarkStdinConsumed tells the vault that stdin carries this command's data,
// so a file-keyring passphrase prompt must use the controlling terminal
// (/dev/tty) or MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE instead. Call it from
// every command that reads its input from stdin, before the first vault
// access. A no-op on hosts with an OS keyring (no prompt ever happens).
func MarkStdinConsumed() { stdinConsumed.Store(true) }

// Injection points for the default passphrase source — package variables so
// tests can drive every branch without a real terminal.
var (
	// passphraseStdin is the process's stdin.
	passphraseStdin io.Reader = os.Stdin
	// passphrasePromptOut receives the prompt when reading from stdin.
	passphrasePromptOut io.Writer = os.Stderr
	// openPassphraseTTY opens the controlling terminal. On a process with
	// none (the desktop app, a daemon) it fails, which is the signal to
	// fall back to the non-interactive source or a clear error.
	openPassphraseTTY = func() (io.ReadWriteCloser, error) {
		return os.OpenFile("/dev/tty", os.O_RDWR, 0)
	}
)

// promptFilePassphrase is the default filePassphraseFunc. Sources, in order:
//
//  1. MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE, when set (explicit
//     configuration wins, and never prompts).
//  2. stdin, when this command has not used stdin for anything else — the
//     original behaviour (interactive prompt, or `printf pass | …`).
//  3. the controlling terminal (/dev/tty), when stdin carries data.
//  4. otherwise a clear error naming option 1.
func promptFilePassphrase() (string, error) {
	if path := os.Getenv(filePassphraseFileEnv); path != "" {
		return readPassphraseFile(path)
	}
	if !stdinConsumed.Load() {
		fmt.Fprint(passphrasePromptOut, "File-keyring passphrase: ")
		pass, err := readPassphraseLine(passphraseStdin, passphrasePromptOut)
		if err != nil {
			return "", err
		}
		if pass == "" {
			return "", fmt.Errorf("secrets: empty file-keyring passphrase (MONOAGENT_ALLOW_FILE_KEYRING=1): type it at the prompt, pipe it on stdin, or %s", filePassphraseHint)
		}
		return pass, nil
	}
	tty, err := openPassphraseTTY()
	if err != nil {
		return "", fmt.Errorf("secrets: the file keyring (MONOAGENT_ALLOW_FILE_KEYRING=1) needs its passphrase, but stdin is carrying this command's input and there is no terminal to prompt on; %s", filePassphraseHint)
	}
	defer tty.Close()
	fmt.Fprint(tty, "File-keyring passphrase: ")
	pass, err := readPassphraseLine(tty, tty)
	if err != nil {
		return "", err
	}
	if pass == "" {
		return "", fmt.Errorf("secrets: empty file-keyring passphrase entered on the terminal; type it at the prompt or %s", filePassphraseHint)
	}
	return pass, nil
}

// readPassphraseLine reads one line from r. When r is a real terminal the
// line is read with echo off (and a newline written to echo, since the
// user's Enter isn't echoed); otherwise a plain line read.
func readPassphraseLine(r io.Reader, echo io.Writer) (string, error) {
	if f, ok := r.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(echo)
		if err != nil {
			return "", fmt.Errorf("secrets: reading file-keyring passphrase: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("secrets: reading file-keyring passphrase: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readPassphraseFile reads the passphrase from the file named by
// MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE: the first line, without its line
// ending. Refuses a file group/others can read (non-Windows).
func readPassphraseFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("secrets: %s: %w", filePassphraseFileEnv, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("secrets: %s=%s is readable by other users (mode %04o); run chmod 600 on it", filePassphraseFileEnv, path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("secrets: %s: %w", filePassphraseFileEnv, err)
	}
	pass, _, _ := strings.Cut(string(data), "\n")
	pass = strings.TrimRight(pass, "\r")
	if pass == "" {
		return "", fmt.Errorf("secrets: %s=%s is empty; put the file-keyring passphrase on its first line", filePassphraseFileEnv, path)
	}
	return pass, nil
}

// knownFilePassphrases remembers, per profile, a passphrase that has already
// unlocked (or created) that profile's file keyring in this process, so one
// command never prompts twice — dek.go's fast-path peek and its locked
// bootstrap can both read the KEK file, and a second stdin read would hit
// EOF. Only verified passphrases are stored.
var (
	knownFilePassphrasesMu sync.Mutex
	knownFilePassphrases   = map[string]string{}
)

// filePassphraseFor returns the remembered passphrase for profileID or asks
// filePassphraseFunc for one.
func filePassphraseFor(profileID string) (string, error) {
	knownFilePassphrasesMu.Lock()
	pass, ok := knownFilePassphrases[profileID]
	knownFilePassphrasesMu.Unlock()
	if ok {
		return pass, nil
	}
	return filePassphraseFunc()
}

func rememberFilePassphrase(profileID, pass string) {
	knownFilePassphrasesMu.Lock()
	knownFilePassphrases[profileID] = pass
	knownFilePassphrasesMu.Unlock()
}

// forgetFilePassphrases clears the in-process cache (tests).
func forgetFilePassphrases() {
	knownFilePassphrasesMu.Lock()
	knownFilePassphrases = map[string]string{}
	knownFilePassphrasesMu.Unlock()
}
