package mcp

import (
	"database/sql"
	"path/filepath"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// grantWorkdir is the directory the bundle's role is confined to in
// monomind, passed to a granted run as org.workdir so its file nodes
// confine to it too (C-46). It is computed from the profile root in the
// database and the org file, never from the provider's environment. An org
// file that cannot be read confines to the profile root, monomind's default
// workspace: a granted run is never left unconfined.
func grantWorkdir(db *sql.DB, b *orggrant.Bundle) string {
	root := profiledir.Root(db, b.ProfileID)
	doc, err := orgdesign.Load(root, b.OrgName)
	if err != nil {
		return filepath.Clean(root)
	}
	return orgdesign.RoleWorkdir(root, doc, b.RoleID)
}
