package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// swapItem replaces target with staged (both on the same filesystem).
type swapItem struct {
	target, staged string
	hadOld         bool
}

// swapAll renames every target to <target>.bak and its staged file into
// place. If any step fails, everything already swapped is put back and the
// staged files are removed, so the installation is either all old or all
// new. The .bak copies are left for the caller to remove (a running exe on
// Windows cannot be deleted).
func swapAll(items []swapItem) error {
	for i := range items {
		it := &items[i]
		bak := it.target + ".bak"
		os.RemoveAll(bak)
		if _, err := os.Lstat(it.target); err == nil {
			if err := os.Rename(it.target, bak); err != nil {
				rollbackSwaps(items[:i])
				removeStaged(items[i:])
				return fmt.Errorf("backup %s: %v", filepath.Base(it.target), err)
			}
			it.hadOld = true
		}
		if err := os.Rename(it.staged, it.target); err != nil {
			if it.hadOld {
				os.Rename(bak, it.target)
			}
			rollbackSwaps(items[:i])
			removeStaged(items[i:])
			return fmt.Errorf("install %s: %v", filepath.Base(it.target), err)
		}
	}
	return nil
}

func rollbackSwaps(done []swapItem) {
	for i := len(done) - 1; i >= 0; i-- {
		it := done[i]
		os.RemoveAll(it.target)
		if it.hadOld {
			os.Rename(it.target+".bak", it.target)
		}
	}
}

func removeStaged(items []swapItem) {
	for _, it := range items {
		os.RemoveAll(it.staged)
	}
}

// stageBytes writes data as an executable temp file in dir.
func stageBytes(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, ".monoagent-update-*")
	if err != nil {
		return "", fmt.Errorf("stage file: %v", err)
	}
	name := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o755)
	}
	if werr != nil {
		os.Remove(name)
		return "", fmt.Errorf("stage file: %v", werr)
	}
	return name, nil
}

// Linux tarball members (release.yml build-linux). Older tarballs carry the
// CLI as monoagentcli-linux-amd64-bundled.
const (
	linuxTarApp       = "MonoAgent-linux-amd64"
	linuxTarCLI       = "monoagentcli"
	linuxTarCLILegacy = "monoagentcli-linux-amd64-bundled"
	maxTarMember      = 512 << 20
)

// readLinuxTarball returns the app and CLI from the verified tarball. Only
// those two regular files are read; anything else is ignored, so no path
// from the archive is ever used on disk.
func readLinuxTarball(tgz []byte) (app, cli []byte, err error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, nil, fmt.Errorf("read tarball: %v", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read tarball: %v", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		var dst *[]byte
		switch filepath.Clean(h.Name) {
		case linuxTarApp:
			dst = &app
		case linuxTarCLI, linuxTarCLILegacy:
			dst = &cli
		default:
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxTarMember+1))
		if err != nil {
			return nil, nil, fmt.Errorf("read tarball: %v", err)
		}
		if len(b) > maxTarMember {
			return nil, nil, fmt.Errorf("tarball member %s is too large", h.Name)
		}
		*dst = b
	}
	if app == nil {
		return nil, nil, fmt.Errorf("%s not found in downloaded archive", linuxTarApp)
	}
	return app, cli, nil
}

// installLinux swaps in the app and — when the tarball has one — the CLI
// next to it (the sibling the app already uses, else monoagentcli), so the
// GUI and its bundled CLI stay on the same version. Staged in the app's
// directory: renames from os.TempDir fail across filesystems.
func installLinux(exe string, tgz []byte) error {
	app, cli, err := readLinuxTarball(tgz)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	stagedApp, err := stageBytes(dir, app)
	if err != nil {
		return err
	}
	items := []swapItem{{target: exe, staged: stagedApp}}
	if cli != nil {
		target, ok := siblingCLI(dir, "linux", "amd64")
		if !ok {
			target = filepath.Join(dir, linuxTarCLI)
		}
		stagedCLI, err := stageBytes(dir, cli)
		if err != nil {
			os.Remove(stagedApp)
			return err
		}
		items = append(items, swapItem{target: target, staged: stagedCLI})
	}
	if err := swapAll(items); err != nil {
		return err
	}
	for _, it := range items {
		os.Remove(it.target + ".bak")
	}
	return nil
}
