package comm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/workflow"
)

// TestCommNodes_RefuseLocalFilesOutsideOrgWorkdir covers the comm nodes that
// read a local file and send it away (C-46): attachments, Slack uploads and
// Telegram photos. Each refusal must come before any network call, so the
// dummy hosts and tokens here are never contacted.
func TestCommNodes_RefuseLocalFilesOutsideOrgWorkdir(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, outside := filepath.Join(base, "work"), filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	ctx := fsconfine.WithRoot(context.Background(), root)

	for _, p := range []string{secret, "../outside/id_rsa", "innocent.txt"} {
		cases := []struct {
			name string
			node workflow.NodeExecutor
			cfg  map[string]interface{}
		}{
			{"email_send", &EmailSendNode{}, map[string]interface{}{
				"smtp_host": "smtp.invalid", "from": "a@b.c", "to": "d@e.f", "subject": "s", "body": "b",
				"attachments": []interface{}{p}}},
			{"outlook_send", &OutlookSendNode{}, map[string]interface{}{
				"email": "a@b.c", "password": "x", "to": "d@e.f", "subject": "s", "body": "b",
				"attachments": []interface{}{p}}},
			{"slack upload_file", &SlackNode{}, map[string]interface{}{
				"access_token": "xoxb-invalid", "operation": "upload_file", "channel": "C1", "file_path": p}},
			{"telegram send_photo", &TelegramNode{}, map[string]interface{}{
				"bot_token": "0:invalid", "operation": "send_photo", "chat_id": "1", "photo_url": p}},
		}
		for _, c := range cases {
			_, err := c.node.Execute(ctx, workflow.NodeInput{}, c.cfg)
			if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
				t.Errorf("%s %q: err = %v, want ErrOutsideWorkdir", c.name, p, err)
			}
		}
	}
}
