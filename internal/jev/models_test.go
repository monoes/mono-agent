package jev

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestModelsListsIDs(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"jev-1.13","object":"model"},{"id":"jev-latest"}]}`))
	})
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/models" || gotAuth != "Bearer k" {
		t.Fatalf("request = %s %s auth %q", gotMethod, gotPath, gotAuth)
	}
	if len(models) != 2 || models[0].ID != "jev-1.13" || models[1].ID != "jev-latest" {
		t.Fatalf("models = %+v", models)
	}
}

func TestModelsAcceptsOtherShapes(t *testing.T) {
	for name, body := range map[string]string{
		"array":   `[{"id":"a"},{"name":"b"}]`,
		"models":  `{"models":[{"id":"a"},{"name":"b"}]}`,
		"strings": `{"data":["a","b"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			models, err := c.Models(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
				t.Fatalf("models = %+v", models)
			}
		})
	}
}

func TestModelsHTTPError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
	})
	c.Retries = 0
	_, err := c.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want HTTP 401", err)
	}
}

func TestModelsUndecodable(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`not json`)) })
	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("want error for undecodable body")
	}
}
