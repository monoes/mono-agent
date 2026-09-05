package monomind

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Mirrors
// SyncToKnowledgeGraph's subprocess/env-var pattern exactly — see that
// function's comment in kgsync.go for why MONOMIND_CWD and creating the
// profile dir first are both required, not optional. Does not pass
// --format json: only the exit code matters here, the inner payload is
// unused (matching SyncToKnowledgeGraph's own fire-and-forget style).
func IngestDocument(ctx context.Context, db *sql.DB, profileID, path string) error {
	bin, err := Find()
	if err != nil {
		return err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("monomind.IngestDocument: creating profile monomind dir: %w", err)
	}

	params, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return fmt.Errorf("monomind.IngestDocument: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_ingest", "-p", string(params))
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("monomind.IngestDocument: knowledge_ingest: %w", err)
	}
	return nil
}
