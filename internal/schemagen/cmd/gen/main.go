// Command gen writes the package JSON Schemas to data/schemas. Run it with
// `go generate ./internal/schemagen` (or `go run ./internal/schemagen/cmd/gen`
// from anywhere inside the module).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/monoes/mono-agent/internal/schemagen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := schemagen.ModuleRoot(wd)
	if err != nil {
		return err
	}
	files, err := schemagen.GenerateAll(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, schemagen.OutDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), files[n], 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", filepath.Join(schemagen.OutDir, n))
	}
	return nil
}
