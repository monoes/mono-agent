package extension

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/capturesummary"
)

// fakeSummaryCatalog is a catalog over a fake scanner and model lister.
func fakeSummaryCatalog(scanErr error) *capturesummary.Catalog {
	return &capturesummary.Catalog{
		Scan: func(context.Context) ([]capturesummary.Runtime, error) {
			if scanErr != nil {
				return nil, scanErr
			}
			return []capturesummary.Runtime{
				{ID: "claude", Version: "2.1.3", Binary: "/home/me/.local/bin/claude"},
				{ID: "opencode"},
			}, nil
		},
		Models: func(_ context.Context, rt capturesummary.Runtime) ([]capturesummary.Model, error) {
			if rt.ID == "claude" {
				return []capturesummary.Model{{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"}}, nil
			}
			return nil, nil
		},
	}
}

func callSummary(t *testing.T, srv *Server, method string, params map[string]any) (any, error) {
	t.Helper()
	h, ok := srv.handlerFor(method)
	if !ok {
		t.Fatalf("%s is not registered", method)
	}
	return h(context.Background(), &Request{Method: method, Params: params}, func(string, string) {})
}

func replyJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

func TestSummaryRuntimesWireShape(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetSummaryCatalog(fakeSummaryCatalog(nil), "claude")

	if m := srv.RequestMethods(); !slices.Contains(m, MethodSummaryRuntimes) || !slices.Contains(m, MethodSummaryModels) {
		t.Fatalf("methods = %v, want both summary methods advertised", m)
	}
	data, err := callSummary(t, srv, MethodSummaryRuntimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The binary path stays on this machine.
	const want = `{"runtimes":[{"id":"claude","version":"2.1.3"},{"id":"opencode"}],"default":"claude","enabled":true}`
	if got := replyJSON(t, data); got != want {
		t.Errorf("reply =\n  %s\nwant\n  %s", got, want)
	}
}

func TestSummaryRuntimesWhenTurnedOff(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetSummaryCatalog(fakeSummaryCatalog(nil), "off")
	data, err := callSummary(t, srv, MethodSummaryRuntimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := data.(*SummaryRuntimes)
	if got.Enabled || got.Default != "" || len(got.Runtimes) != 2 {
		t.Errorf("reply = %+v, want the list with enabled=false and no default", got)
	}
}

func TestSummaryRuntimesScanFailureIsUnavailable(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetSummaryCatalog(fakeSummaryCatalog(errors.New("monomind not found")), "claude")
	_, err := callSummary(t, srv, MethodSummaryRuntimes, nil)
	var re *RequestError
	if !errors.As(err, &re) || re.Code != CodeUnavailable {
		t.Fatalf("err = %v, want code %s", err, CodeUnavailable)
	}
}

func TestSummaryModels(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetSummaryCatalog(fakeSummaryCatalog(nil), "claude")

	data, err := callSummary(t, srv, MethodSummaryModels, map[string]any{"runtime": "claude"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"runtime":"claude","models":[{"id":"claude-haiku-4-5-20251001","label":"Haiku 4.5"}]}`
	if got := replyJSON(t, data); got != want {
		t.Errorf("reply =\n  %s\nwant\n  %s", got, want)
	}

	// No list is an empty array, never null: the picker then offers free text.
	data, err = callSummary(t, srv, MethodSummaryModels, map[string]any{"runtime": "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if got := replyJSON(t, data); got != `{"runtime":"opencode","models":[]}` {
		t.Errorf("opencode reply = %s", got)
	}

	for _, bad := range []any{"codex", "../../bin/sh", "", 42} {
		_, err := callSummary(t, srv, MethodSummaryModels, map[string]any{"runtime": bad})
		var re *RequestError
		if !errors.As(err, &re) || re.Code != CodeInvalid {
			t.Errorf("runtime %v: err = %v, want code %s", bad, err, CodeInvalid)
		}
	}
}

func TestSetSummaryCatalogNilRemovesTheMethods(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetSummaryCatalog(fakeSummaryCatalog(nil), "claude")
	srv.SetSummaryCatalog(nil, "")
	if m := srv.RequestMethods(); slices.Contains(m, MethodSummaryRuntimes) || slices.Contains(m, MethodSummaryModels) {
		t.Errorf("methods = %v after removal", m)
	}
}

// A Go-initiated capture carries the choice to the extension, and the relay
// hop keeps it.
func TestCaptureRequestCarriesTheSummaryChoice(t *testing.T) {
	req := CaptureRequest{Mode: "summary", SummaryRuntime: "claude", SummaryModel: "claude-haiku-4-5-20251001"}
	cmd := req.command()
	if cmd.Params["summaryRuntime"] != "claude" || cmd.Params["summaryModel"] != "claude-haiku-4-5-20251001" {
		t.Fatalf("params = %v", cmd.Params)
	}
	blob, _ := json.Marshal(cmd)
	var back Command
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	got := captureRequestFromCommand(&back, 0, "")
	if got.SummaryRuntime != "claude" || got.SummaryModel != "claude-haiku-4-5-20251001" {
		t.Errorf("relayed request = %+v", got)
	}
	if _, ok := (CaptureRequest{Mode: "full"}).command().Params["summaryRuntime"]; ok {
		t.Error("an unset choice must not be sent")
	}
}
