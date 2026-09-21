package extension

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCaptureRequestRoundTripsThroughACommand(t *testing.T) {
	req := CaptureRequest{
		TabID:      5,
		Formats:    []string{"mhtml", "readable"},
		Selection:  true,
		Note:       "why this matters",
		Tags:       []string{"a", "b"},
		Collection: "reading",
	}
	// Through JSON, as the relay hop puts it.
	blob, err := json.Marshal(req.command())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cmd Command
	if err := json.Unmarshal(blob, &cmd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := captureRequestFromCommand(&cmd, 42*time.Second, "/tmp/elsewhere")
	if got.TabID != 5 || !got.Selection || got.Note != "why this matters" || got.Collection != "reading" {
		t.Fatalf("round-tripped = %+v", got)
	}
	if len(got.Formats) != 2 || got.Formats[1] != "readable" {
		t.Fatalf("formats = %v", got.Formats)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "a" {
		t.Fatalf("tags = %v", got.Tags)
	}
	if got.timeout() != 42*time.Second {
		t.Fatalf("timeout = %s", got.timeout())
	}
	if got.Inbox != "/tmp/elsewhere" {
		t.Fatalf("inbox = %q", got.Inbox)
	}
	// The extension must never be handed a filesystem path.
	if _, leaked := cmd.Params["inbox"]; leaked {
		t.Fatalf("inbox leaked into the extension-facing params: %v", cmd.Params)
	}
}

func TestCaptureRequestDefaults(t *testing.T) {
	req := CaptureRequest{}
	if got := req.timeout(); got != DefaultCaptureTimeout {
		t.Fatalf("timeout = %s", got)
	}
	if got := req.formats(); len(got) != 4 {
		t.Fatalf("formats = %v", got)
	}
	cmd := req.command()
	if cmd.ID == "" {
		t.Fatal("command has no id")
	}
	if tags, ok := cmd.Params["tags"].([]string); !ok || tags == nil {
		t.Fatalf("params.tags = %v, want an empty slice not null", cmd.Params["tags"])
	}
}

func TestIsCaptureResponse(t *testing.T) {
	cases := []struct {
		name string
		resp *Response
		want bool
	}{
		{"typed failure", &Response{Type: CmdPageCapture, Error: "x"}, true},
		{"chunk by shape", &Response{Data: map[string]any{"chunk": map[string]any{}}}, true},
		{"final by shape", &Response{Data: map[string]any{"meta": map[string]any{}, "artifacts": []any{}}}, true},
		{"meta alone is not enough", &Response{Data: map[string]any{"meta": map[string]any{}}}, false},
		{"ordinary response", &Response{Data: map[string]any{"result": "42"}}, false},
		{"no data", &Response{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCaptureResponse(tc.resp); got != tc.want {
				t.Fatalf("isCaptureResponse = %v, want %v", got, tc.want)
			}
		})
	}
}
