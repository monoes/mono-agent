package nodes

import "testing"

func TestNormalizeBrowserItem(t *testing.T) {
	raw := map[string]interface{}{
		"text": "Alice Wonderland\nCTO at StartupCo\nBerlin",
		"href": "https://www.linkedin.com/in/alice-wonderland/",
	}
	out := NormalizeBrowserItem(raw, "linkedin")

	cases := []struct {
		key  string
		want string
	}{
		{"profile_url", "https://www.linkedin.com/in/alice-wonderland/"},
		{"url", "https://www.linkedin.com/in/alice-wonderland/"},
		{"full_name", "Alice Wonderland"},
		{"job_title", "CTO at StartupCo"},
		{"platform", "linkedin"},
	}
	for _, c := range cases {
		got, _ := out[c.key].(string)
		if got != c.want {
			t.Errorf("NormalizeBrowserItem[%q]: want %q, got %q", c.key, c.want, got)
		}
	}
}

func TestNormalizeBrowserItem_PreservesExistingFields(t *testing.T) {
	raw := map[string]interface{}{
		"profile_url": "https://existing.url/",
		"full_name":   "Already Set",
		"platform":    "instagram",
		"href":        "https://other.url/",
	}
	out := NormalizeBrowserItem(raw, "linkedin")

	// Should NOT overwrite existing values.
	if out["profile_url"] != "https://existing.url/" {
		t.Errorf("profile_url overwritten: %v", out["profile_url"])
	}
	if out["full_name"] != "Already Set" {
		t.Errorf("full_name overwritten: %v", out["full_name"])
	}
	if out["platform"] != "instagram" {
		t.Errorf("platform overwritten: %v", out["platform"])
	}
}

// The Gemini multi-image node's real output, before this: a run that saved a
// 1MB image reported "skipped":true and "reason":"empty session_id" beside
// "image_count":1, because navigate_to_session's no-op map was merged in
// wholesale. Step bookkeeping must never become the node's answer.
func TestMergeStepResultsDropsStepBookkeeping(t *testing.T) {
	inputJSON := map[string]interface{}{"prompts": []interface{}{"a wizard casting a fire spell"}}
	extracted := []map[string]interface{}{
		// navigate_to_session with no session_id supplied.
		{"success": true, "skipped": true, "reason": "empty session_id"},
		// type_prompt reporting which selector worked.
		{"success": true, "selector": "div.ql-editor[contenteditable='true']", "typed": 44},
		// extract_response_by_mode — the step that actually produced something.
		{"success": true, "image_count": 1, "session_id": "f6f594bd8c117c3d",
			"images": []interface{}{map[string]interface{}{"vault_id": "img-001"}}},
	}

	merged := mergeStepResults(inputJSON, extracted, "gemini")

	for _, gone := range []string{"skipped", "reason", "selector", "typed", "method", "ready"} {
		if _, present := merged[gone]; present {
			t.Errorf("%q is step bookkeeping and must not reach the node's output: %v", gone, merged[gone])
		}
	}
	if merged["image_count"] != 1 {
		t.Errorf("image_count should survive, got %v", merged["image_count"])
	}
	if merged["session_id"] != "f6f594bd8c117c3d" {
		t.Errorf("session_id should survive, got %v", merged["session_id"])
	}
	if merged["images"] == nil {
		t.Error("images should survive")
	}
	if merged["prompts"] == nil {
		t.Error("the input item's own fields should still be carried through")
	}
	if merged["platform"] != "gemini" {
		t.Errorf("platform should be set, got %v", merged["platform"])
	}
	// success is deliberately kept — a workflow may already branch on it.
	if merged["success"] != true {
		t.Errorf("success should still surface, got %v", merged["success"])
	}
}

// When every step was a no-op there is nothing to report but the input, and
// saying so beats echoing a skip map as though it were a result.
func TestMergeStepResultsWithOnlySkippedSteps(t *testing.T) {
	merged := mergeStepResults(
		map[string]interface{}{"prompts": []interface{}{"x"}},
		[]map[string]interface{}{{"success": true, "skipped": true, "reason": "empty session_id"}},
		"gemini",
	)
	if _, present := merged["skipped"]; present {
		t.Errorf("a skipped step must not become the output: %v", merged)
	}
	if merged["prompts"] == nil {
		t.Error("input fields should still come through")
	}
	if _, present := merged["image_count"]; present {
		t.Error("nothing was produced, so no result fields should appear")
	}
}
