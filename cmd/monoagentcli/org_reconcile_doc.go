package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// newOrgReconcileDocCmd reconciles one org document against the profile's
// enforcement rows without writing it — the row half of an editor's save.
// The desktop app's Org Designer writes the document itself (it has to
// tell its file watcher about its own write, and validates before touching
// rows so a rejected save can still roll the file back), so it sends the
// document here, then saves what comes back.
func newOrgReconcileDocCmd(env *orgEnv) *cobra.Command {
	var isNew bool
	var by string
	c := &cobra.Command{
		Use:   "reconcile-doc <name>",
		Short: "Reconcile an org document (JSON on stdin) with its grant, endpoint, and autonomy rows and print it, without saving",
		Long: "Reads a full org document on stdin and rewrites its generated blocks (tool providers, approval " +
			"tools, endpoint URLs) from the profile's rows, dropping any grant or provider no row backs; " +
			"prints {\"v\":1,\"org\":<document>,\"reconcile\":[findings]}. With --new it first stores the " +
			"org's starting autonomy (mid, or manual when the document asks for it). Nothing is written to " +
			"the org file.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("reading the document from stdin: %w", err)
			}
			var d orgdesign.Doc
			if err := json.Unmarshal(raw, &d); err != nil {
				return errInvalidInput("invalid org document on stdin: %v", err)
			}
			if d.Name == "" {
				d.Name = name
			} else if d.Name != name {
				return errInvalidInput("org name %q in the document does not match the <name> argument %q", d.Name, name)
			}
			db, profileID, _, err := env.Profile()
			if err != nil {
				return err
			}
			if isNew {
				if err := ensureNewOrgAutonomy(cmd.Context(), db, profileID, &d, by); err != nil {
					return err
				}
			}
			rep, err := reconcileOrgRows(cmd.Context(), db, profileID, &d, env.genOptions(profileID))
			if err != nil {
				return err
			}
			findings := []orggrant.Finding{}
			if rep != nil && rep.Findings != nil {
				findings = rep.Findings
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": &d, "reconcile": findings})
		},
	}
	c.Flags().BoolVar(&isNew, "new", false, "The org is new: store its starting autonomy first")
	c.Flags().StringVar(&by, "by", "cli", "Who the starting autonomy is recorded as set by")
	return c
}
