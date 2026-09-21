package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/profiledir"
)

func profileServer(t *testing.T, src ProfileSource) *Server {
	t.Helper()
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetProfileSource(src)
	return srv
}

func callProfileList(t *testing.T, srv *Server) (any, error) {
	t.Helper()
	handler, ok := srv.handlerFor(MethodProfileList)
	if !ok {
		t.Fatalf("%s is not registered", MethodProfileList)
	}
	return handler(context.Background(), &Request{Method: MethodProfileList}, func(string, string) {})
}

func TestProfileList_ReturnsProfilesAndDefault(t *testing.T) {
	srv := profileServer(t, func(context.Context) ([]profiledir.Profile, error) {
		return []profiledir.Profile{
			{ID: "p1", Name: "Personal"},
			{ID: "p2", Name: "Work", Default: true},
		}, nil
	})

	data, err := callProfileList(t, srv)
	if err != nil {
		t.Fatalf("profile.list: %v", err)
	}
	list, ok := data.(*ProfileList)
	if !ok {
		t.Fatalf("data = %T, want *ProfileList", data)
	}
	if len(list.Profiles) != 2 || list.Profiles[1].Name != "Work" {
		t.Fatalf("profiles = %+v", list.Profiles)
	}
	if list.Default != "p2" {
		t.Errorf("default = %q, want p2", list.Default)
	}

	// The wire shape is part of the contract with the extension.
	blob, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"profiles":[{"id":"p1","name":"Personal","default":false},{"id":"p2","name":"Work","default":true}],"default":"p2"}`
	if string(blob) != want {
		t.Errorf("reply JSON =\n  %s\nwant\n  %s", blob, want)
	}
}

// No profiles configured is an answer, not a failure: the picker shows
// nothing to choose and the capture still saves.
func TestProfileList_EmptyIsAnAnswer(t *testing.T) {
	srv := profileServer(t, func(context.Context) ([]profiledir.Profile, error) { return nil, nil })

	data, err := callProfileList(t, srv)
	if err != nil {
		t.Fatalf("profile.list: %v", err)
	}
	blob, _ := json.Marshal(data)
	if string(blob) != `{"profiles":[]}` {
		t.Errorf("reply JSON = %s, want an empty list", blob)
	}
}

// A source that cannot answer is "unavailable" — the code the extension
// renders as quiet absence rather than a red box.
func TestProfileList_SourceFailureIsUnavailable(t *testing.T) {
	srv := profileServer(t, func(context.Context) ([]profiledir.Profile, error) {
		return nil, errors.New("database is locked")
	})

	_, err := callProfileList(t, srv)
	if err == nil {
		t.Fatal("expected an error")
	}
	var re *RequestError
	if !asRequestError(err, &re) {
		t.Fatalf("error %v is not a *RequestError", err)
	}
	if re.Code != CodeUnavailable {
		t.Errorf("code = %q, want %q", re.Code, CodeUnavailable)
	}
}

// The method is advertised exactly when it can be answered: an extension
// probing `ping` must not see a method that would always fail.
func TestProfileList_AdvertisedOnlyWithASource(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	if hasMethod(srv.RequestMethods(), MethodProfileList) {
		t.Fatal("profile.list advertised with no source")
	}

	srv.SetProfileSource(func(context.Context) ([]profiledir.Profile, error) { return nil, nil })
	if !hasMethod(srv.RequestMethods(), MethodProfileList) {
		t.Fatal("profile.list not advertised after SetProfileSource")
	}

	srv.SetProfileSource(nil)
	if hasMethod(srv.RequestMethods(), MethodProfileList) {
		t.Fatal("profile.list still advertised after the source was removed")
	}
}

func hasMethod(methods []string, want string) bool {
	for _, m := range methods {
		if m == want {
			return true
		}
	}
	return false
}
