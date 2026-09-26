package vault

import (
	"context"
	"testing"
)

// The desktop app's /vault-image/ file server only serves the active
// profile's files.
func TestImageFileInProfile(t *testing.T) {
	db := newVaultTestDB(t)
	if _, err := db.Exec(`INSERT INTO vault_images (id, path, filename, profile_id) VALUES ('img-001', '/v/img-001.png', 'img-001.png', 'p1')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, c := range []struct {
		profile, file string
		want          bool
	}{
		{"p1", "img-001.png", true},
		{"p2", "img-001.png", false},
		{"p1", "img-002.png", false},
	} {
		got, err := ImageFileInProfile(ctx, db, c.profile, c.file)
		if err != nil || got != c.want {
			t.Errorf("ImageFileInProfile(%s, %s) = %v, %v; want %v", c.profile, c.file, got, err, c.want)
		}
	}
}
