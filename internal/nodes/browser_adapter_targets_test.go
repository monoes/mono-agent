package nodes

import (
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// linkedin.list_user_posts declares its required list as "targets"; the node
// passes the list under both names, so validation must accept it.
func TestValidateAcceptsTargetsAlias(t *testing.T) {
	items := []interface{}{map[string]interface{}{"url": "https://www.linkedin.com/in/someone/"}}
	err := action.ValidateActionInputs("linkedin", "list_user_posts", &action.StorageAction{},
		map[string]interface{}{"selectedListItems": items, "targets": items})
	if err != nil {
		t.Fatalf("validation with targets = %v", err)
	}
	err = action.ValidateActionInputs("linkedin", "list_user_posts", &action.StorageAction{},
		map[string]interface{}{"selectedListItems": items})
	if err == nil {
		t.Fatal("without the targets alias the old failure should reproduce")
	}
}
