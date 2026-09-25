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

func TestRecordItemsKeepEveryRecordAndCommentText(t *testing.T) {
	items := recordItems([]map[string]interface{}{
		{"author": "alice", "text": "Great post • really", "depth": 0},
		{"author": "bob", "text": "22 likes", "selector_used": "x"},
		{"skipped": true},
	}, "hackernews")
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if _, ok := items[0].JSON["full_name"]; ok {
		t.Errorf("a comment's text became a name: %v", items[0].JSON)
	}
	if items[0].JSON["text"] != "Great post • really" {
		t.Errorf("text changed: %v", items[0].JSON["text"])
	}
	// A bare profile card still gets split.
	card := NormalizeBrowserItem(map[string]interface{}{"text": "Jane Doe • 2nd\nEngineer", "href": "https://x"}, "linkedin")
	if card["full_name"] != "Jane Doe" {
		t.Errorf("card full_name = %v", card["full_name"])
	}
}
