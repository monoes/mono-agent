package nodemgr

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// safeJoin resolves an archive entry name under dir, rejecting escapes.
func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if p != dir && !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the target folder", name)
	}
	return p, nil
}

// Archives are unpacked through an os.Root on the staging folder, which
// refuses to follow a symlink out of it, and an entry whose parent path
// holds a symlink is refused outright: safeJoin checks names as text only,
// and a chain of links made earlier in the same archive (x/y -> .., then
// x/y/z -> .., then x/y/z/f) used to put f outside the folder.

// budget counts the bytes an archive unpacks to and fails past its limit,
// whatever sizes the archive's headers claim.
type budget struct{ left int64 }

func (b *budget) Write(p []byte) (int, error) {
	if b.left -= int64(len(p)); b.left < 0 {
		return 0, errors.New("archive unpacks to more than the size limit")
	}
	return len(p), nil
}

func untarGz(archive, dir string, limit int64) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	b := &budget{left: limit}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := entryPath(root, dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(root, rel, tr, os.FileMode(hdr.Mode).Perm(), b); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links must stay inside the tree (npm/npx → ../lib/...).
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("absolute symlink %q in archive", hdr.Name)
			}
			if _, err := safeJoin(dir, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(hdr.Linkname, rel); err != nil {
				return err
			}
		}
	}
}

func unzip(archive, dir string, limit int64) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	b := &budget{left: limit}
	for _, zf := range zr.File {
		rel, err := entryPath(root, dir, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(root, rel, rc, 0o755, b)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// entryPath is an archive entry's path relative to the staging folder,
// refused when it escapes as text (safeJoin) or when a folder on its way is
// a symlink the archive made.
func entryPath(root *os.Root, dir, name string) (string, error) {
	target, err := safeJoin(dir, name)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(rel)
	if parent == "." {
		return rel, nil
	}
	prefix := ""
	for _, part := range strings.Split(parent, string(os.PathSeparator)) {
		prefix = filepath.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			break // not made yet: nothing below it can be a link either
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("archive entry %q goes through the symlink %q", name, prefix)
		}
	}
	return rel, nil
}

func writeFile(root *os.Root, rel string, r io.Reader, mode os.FileMode, b *budget) error {
	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.MultiWriter(b, f), r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
