//go:build !nosocial

package tiktok

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// export_followers requires the profile (G2), and still accepts it from
// the node's targets, as older workflows set it.
func TestExportFollowersRequiresProfile(t *testing.T) {
	err := action.ValidateActionInputs("tiktok", "export_followers", &action.StorageAction{Params: map[string]interface{}{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "profileUrl") {
		t.Fatalf("no profile: err = %v", err)
	}
	for name, extras := range map[string]map[string]interface{}{
		"targets":    {"targets": []interface{}{map[string]interface{}{"url": "https://www.tiktok.com/@nasa"}}},
		"profileUrl": {"profileUrl": "https://www.tiktok.com/@nasa"},
	} {
		if err := action.ValidateActionInputs("tiktok", "export_followers", &action.StorageAction{Params: map[string]interface{}{}}, extras); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
