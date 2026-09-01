// Command schemagen regenerates internal/workflow/schemas/<node-type>.json
// for every node type listed in internal/tools/schemagen.Manifest, from the
// `schema:"..."` struct tags on that node's companion schema struct. See
// internal/tools/schemagen's package doc for the tag grammar and
// internal/workflow/schemas/README.md for the generated-vs-hand-written
// convention.
//
// Usage:
//
//	go run ./cmd/schemagen           # regenerate and write every manifest entry
//	go run ./cmd/schemagen -check    # exit 1 if regenerating would change any file (CI mode)
//
// go:generate directive lives on the manifest itself; run this from the
// repo root (it locates the repo root via go.mod, so any cwd under the repo
// works too).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/tools/schemagen"
)

func main() {
	check := flag.Bool("check", false, "don't write files; exit 1 if any generated file would differ from what's on disk")
	flag.Parse()

	root, err := schemagen.RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}

	var drift []string
	for _, entry := range schemagen.Manifest {
		out, err := schemagen.RenderJSON(root, entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "schemagen: %s: %v\n", entry.NodeType, err)
			os.Exit(1)
		}
		path := schemagen.SchemaPath(root, entry)

		if *check {
			existing, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "schemagen: %s: read %s: %v\n", entry.NodeType, path, err)
				os.Exit(1)
			}
			if !bytes.Equal(existing, out) {
				drift = append(drift, entry.NodeType)
			}
			continue
		}

		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "schemagen: %s: write %s: %v\n", entry.NodeType, path, err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}

	if *check && len(drift) > 0 {
		fmt.Fprintln(os.Stderr, "schemagen: generated schema out of date for:", drift)
		fmt.Fprintln(os.Stderr, "run `go run ./cmd/schemagen` and commit the result")
		os.Exit(1)
	}
}
