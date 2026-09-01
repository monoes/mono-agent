package action

import "testing"

// TestResolvePathCountAndLength is a regression test: "{{variable.count}}" and
// "{{variable.length}}" templates previously only worked when the variable
// came from a tracked step result (resolveStepResultField's special-cased
// "count" branch). For any variable set via plain SetVariable (e.g. a
// call_bot_method result stored under variable_name, not a step id), these
// templates silently resolved to nil/empty — affecting several action JSONs
// (instagram/linkedin's list_post_comments.count, tiktok's list_video_comments
// .length, etc.) and the linkedin/x publish_post "media.length > 0" condition,
// which was permanently false because "media" is a plain string variable.
func TestResolvePathCountAndLength(t *testing.T) {
	ctx := NewExecutionContext()
	ctx.SetVariable("comments", []interface{}{"a", "b", "c"})
	ctx.SetVariable("media", "/tmp/photo.jpg")
	ctx.SetVariable("emptyMedia", "")

	vr := NewVariableResolver(ctx)

	if got := vr.ResolvePath("comments.count"); got != 3 {
		t.Errorf("comments.count = %v, want 3", got)
	}
	if got := vr.ResolvePath("media.length"); got != len("/tmp/photo.jpg") {
		t.Errorf("media.length = %v, want %d", got, len("/tmp/photo.jpg"))
	}
	if got := vr.ResolvePath("emptyMedia.length"); got != 0 {
		t.Errorf("emptyMedia.length = %v, want 0", got)
	}
}

// TestResolveStepDefUploadJoinsArrayMedia is a regression test: schemas that
// declare "media" as an array (e.g. facebook.json) previously broke the
// "upload" step because ResolveStepDef stringified the resolved []string via
// fmt.Sprintf("%v", ...), producing "[/a.png /b.png]" — not a valid path and
// not splittable by stepUpload's comma-based parser. Array-valued media
// should be joined with "," to match what stepUpload expects.
func TestResolveStepDefUploadJoinsArrayMedia(t *testing.T) {
	ctx := NewExecutionContext()
	ctx.SetVariable("media", []string{"/a.png", "/b.png"})
	vr := NewVariableResolver(ctx)

	step := StepDef{ID: "upload_media", Type: "upload", Text: "{{media}}"}
	resolved := vr.ResolveStepDef(step)

	if resolved.Text != "/a.png,/b.png" {
		t.Errorf("Text = %q, want %q", resolved.Text, "/a.png,/b.png")
	}
}

// TestResolveStepDefUploadSingleStringMedia ensures the array-join special
// case for "upload" steps doesn't regress the common case of a single-path
// string value.
func TestResolveStepDefUploadSingleStringMedia(t *testing.T) {
	ctx := NewExecutionContext()
	ctx.SetVariable("media", "/tmp/photo.jpg")
	vr := NewVariableResolver(ctx)

	step := StepDef{ID: "upload_media", Type: "upload", Text: "{{media}}"}
	resolved := vr.ResolveStepDef(step)

	if resolved.Text != "/tmp/photo.jpg" {
		t.Errorf("Text = %q, want %q", resolved.Text, "/tmp/photo.jpg")
	}
}

// TestResolveStepDefNonUploadStepStillStringifiesArrays confirms the
// array-join special case is scoped to "upload" steps only — other step
// types keep the pre-existing fmt.Sprintf("%v", ...) stringification.
func TestResolveStepDefNonUploadStepStillStringifiesArrays(t *testing.T) {
	ctx := NewExecutionContext()
	ctx.SetVariable("tags", []string{"a", "b"})
	vr := NewVariableResolver(ctx)

	step := StepDef{ID: "log_tags", Type: "log", Text: "{{tags}}"}
	resolved := vr.ResolveStepDef(step)

	if resolved.Text != "[a b]" {
		t.Errorf("Text = %q, want %q (unchanged fmt.Sprintf stringification for non-upload steps)", resolved.Text, "[a b]")
	}
}
