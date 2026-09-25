package jevtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/jev"
)

func TestFakeAnswersAllTypesValidly(t *testing.T) {
	srv := NewServer(t, Fixed(map[string]string{"route": "b", "spam": "0.8", "level": "2"}))
	c, err := jev.NewClient("k", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Ask(context.Background(), map[string]any{"secret": "s3cr3t"}, map[string]jev.Question{
		"route": {Type: jev.TypeChoice, Criteria: map[string]any{"a": "A", "b": "B", "c": "C"}},
		"spam":  {Type: jev.TypeNoul},
		"level": {Type: jev.TypeScore, Criteria: []string{"low", "mid", "high"}},
		"other": {Type: jev.TypeChoice, Criteria: map[string]any{"y": 1, "x": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, p := jev.Top(resp.Answers["route"]); id != "b" || p != 0.94 {
		t.Errorf("route top = %s %v", id, p)
	}
	if id, p := jev.Top(resp.Answers["spam"]); id != "true" || p != 0.8 {
		t.Errorf("spam = %s %v", id, p)
	}
	if resp.Answers["level"].Score != 2 || resp.Answers["other"].Choice != "x" {
		t.Errorf("level=%v other=%v", resp.Answers["level"].Score, resp.Answers["other"].Choice)
	}
	if srv.Calls() != 1 || !strings.Contains(srv.RequestJSON(), "s3cr3t") {
		t.Errorf("calls=%d json=%s", srv.Calls(), srv.RequestJSON())
	}
	if m := jev.Margin(resp.Answers["route"]); m < 0.9 {
		t.Errorf("margin = %v", m)
	}
}

func TestStatusAndOnResult(t *testing.T) {
	srv := NewServer(t, nil)
	srv.SetStatus(401)
	c, _ := jev.NewClient("k", "")
	var got []error
	c.OnResult = func(req *jev.Request, resp *jev.Response, _ time.Duration, err error) {
		if req == nil || len(req.Questions) != 1 {
			t.Errorf("OnResult req = %+v", req)
		}
		got = append(got, err)
	}
	q := map[string]jev.Question{"q": {Type: jev.TypeChoice, Criteria: map[string]any{"a": nil}}}
	if _, err := c.Ask(context.Background(), "s", q); err == nil {
		t.Fatal("want HTTP 401 error")
	}
	srv.SetStatus(0)
	if _, err := c.Ask(context.Background(), "s", q); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] == nil || got[1] != nil {
		t.Fatalf("OnResult errors = %v", got)
	}
}

func TestBadNoulFromHandlerIsAnError(t *testing.T) {
	NewServer(t, Fixed(map[string]string{"n": "lots"}))
	c, _ := jev.NewClient("k", "")
	_, err := c.Ask(context.Background(), "s", map[string]jev.Question{"n": {Type: jev.TypeNoul}})
	if err == nil || errors.Is(err, jev.ErrInvalidAnswer) {
		t.Fatalf("err = %v, want an HTTP 422 from the fake", err)
	}
}

func TestFakeNoulZeroRoundTrips(t *testing.T) {
	NewServer(t, Fixed(map[string]string{"spam": "0"}))
	c, err := jev.NewClient("k", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Ask(context.Background(), "s", map[string]jev.Question{"spam": {Type: jev.TypeNoul}})
	if err != nil {
		t.Fatal(err)
	}
	if id, p := jev.Top(resp.Answers["spam"]); id != "true" || p != 0 {
		t.Fatalf("spam = %s %v", id, p)
	}
}
