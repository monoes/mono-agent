package extension

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

// fakeTaskSink records what task.add hands it and answers as told. The mutex
// is for the socket test, where the handler runs on the server's goroutine.
type fakeTaskSink struct {
	mu    sync.Mutex
	got   []CapturedTask
	reply TaskAdded
	err   error
}

func (f *fakeTaskSink) AddCaptured(_ context.Context, t CapturedTask) (TaskAdded, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, t)
	return f.reply, f.err
}

func (f *fakeTaskSink) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func taskServer(t *testing.T, sink TaskSink) *Server {
	t.Helper()
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetTaskSink(sink)
	return srv
}

// callTaskAdd runs task.add's handler as the dispatcher does, for a browser
// with the given label.
func callTaskAdd(t *testing.T, srv *Server, label string, params map[string]any) (any, error) {
	t.Helper()
	h, ok := srv.handlerFor(MethodTaskAdd)
	if !ok {
		t.Fatalf("%s is not registered", MethodTaskAdd)
	}
	req := &Request{Method: MethodTaskAdd, Params: params, Origin: ConnInfo{Label: label}}
	return h(context.Background(), req, func(string, string) {})
}

func taskParams(over map[string]any) map[string]any {
	p := map[string]any{
		"client_id": "t-0001", "profile": "p-work", "kind": "selection",
		"text": "Reply to Sam", "url": "https://mail.example/x", "title": "Inbox",
	}
	for k, v := range over {
		p[k] = v
	}
	return p
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var re *RequestError
	if !asRequestError(err, &re) {
		t.Fatalf("error %v is not a *RequestError", err)
	}
	return re.Code
}

// The method is advertised exactly when something can answer it: a newer
// extension against a host with no sink keeps its task queued.
func TestTaskAdd_AdvertisedOnlyWithASink(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	if hasMethod(srv.RequestMethods(), MethodTaskAdd) {
		t.Fatal("task.add advertised with no sink")
	}
	srv.SetTaskSink(&fakeTaskSink{})
	if !hasMethod(srv.RequestMethods(), MethodTaskAdd) {
		t.Fatal("task.add not advertised after SetTaskSink")
	}
	srv.SetTaskSink(nil)
	if hasMethod(srv.RequestMethods(), MethodTaskAdd) {
		t.Fatal("task.add still advertised after the sink was removed")
	}
}

func TestTaskAdd_HandsTheSinkTheRequestAndTheBrowser(t *testing.T) {
	sink := &fakeTaskSink{reply: TaskAdded{ID: 12, Created: true}}
	data, err := callTaskAdd(t, taskServer(t, sink), "Edge Work", taskParams(nil))
	if err != nil {
		t.Fatalf("task.add: %v", err)
	}
	want := CapturedTask{ProfileID: "p-work", ClientID: "t-0001", Kind: TaskKindSelection,
		Text: "Reply to Sam", URL: "https://mail.example/x", Title: "Inbox", Origin: "Edge Work"}
	if len(sink.got) != 1 || sink.got[0] != want {
		t.Fatalf("sink got %+v, want %+v", sink.got, want)
	}
	blob, _ := json.Marshal(data)
	if string(blob) != `{"id":12,"created":true}` {
		t.Errorf("reply JSON = %s", blob)
	}
}

func TestTaskAdd_AnUnlabelledBrowserIsChrome(t *testing.T) {
	sink := &fakeTaskSink{}
	if _, err := callTaskAdd(t, taskServer(t, sink), "", taskParams(nil)); err != nil {
		t.Fatal(err)
	}
	if sink.got[0].Origin != "Chrome" {
		t.Errorf("origin %q, want Chrome", sink.got[0].Origin)
	}
}

func TestTaskAdd_RefusesWhatNoBoardCouldFile(t *testing.T) {
	cases := []struct {
		name string
		over map[string]any
		want string
	}{
		{"no profile", map[string]any{"profile": ""}, "needs a profile"},
		{"a blank profile", map[string]any{"profile": "   "}, "needs a profile"},
		{"a profile that is not a string", map[string]any{"profile": 7}, "needs a profile"},
		{"no client id", map[string]any{"client_id": ""}, "needs a client_id"},
		{"no kind", map[string]any{"kind": ""}, "is not selection, page or note"},
		{"an unknown kind", map[string]any{"kind": "screenshot"}, "is not selection, page or note"},
	}
	for _, c := range cases {
		sink := &fakeTaskSink{}
		_, err := callTaskAdd(t, taskServer(t, sink), "", taskParams(c.over))
		if err == nil || codeOf(t, err) != CodeInvalidInput || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want invalid_input mentioning %q", c.name, err, c.want)
		}
		if len(sink.got) != 0 {
			t.Errorf("%s: the sink was called", c.name)
		}
	}
}

func TestTaskAdd_ANoteIsAboutNoPage(t *testing.T) {
	sink := &fakeTaskSink{}
	if _, err := callTaskAdd(t, taskServer(t, sink), "", taskParams(map[string]any{"kind": "note"})); err != nil {
		t.Fatal(err)
	}
	if got := sink.got[0]; got.URL != "" || got.Title != "" || got.Text != "Reply to Sam" || got.Kind != TaskKindNote {
		t.Errorf("a note kept page fields: %+v", got)
	}
}

// The sink's codes reach the extension unchanged: its outbox drops an entry
// on invalid_input, keeps one on limit, and waits on unavailable.
func TestTaskAdd_TheSinksCodesReachTheExtension(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"unknown profile", &RequestError{Code: CodeInvalidInput, Err: errors.New(`invalid input: unknown profile "gone"`)}, CodeInvalidInput},
		{"full board", &RequestError{Code: CodeLimit, Err: errors.New("limit reached")}, CodeLimit},
		{"no database", Unavailable("no monoagent database"), CodeUnavailable},
	} {
		_, err := callTaskAdd(t, taskServer(t, &fakeTaskSink{err: c.err}), "", taskParams(nil))
		if err == nil || codeOf(t, err) != c.want {
			t.Errorf("%s: err %v, want code %s", c.name, err, c.want)
		}
	}
}

// Over a real socket: the reply's shape, and a plain sink error as internal.
func TestTaskAdd_OverTheSocket(t *testing.T) {
	sink := &fakeTaskSink{reply: TaskAdded{ID: 7, Created: false}}
	_, ext, _ := startCaptureServerWith(t, func(s *Server) { s.SetTaskSink(sink) })

	ext.ask("req-t1", MethodTaskAdd, taskParams(nil))
	reply := ext.settled()
	if !reply.OK {
		t.Fatalf("task.add failed: %s (%s)", reply.Error, reply.Code)
	}
	data, _ := reply.Data.(map[string]any)
	if data["id"] != float64(7) || data["created"] != false {
		t.Errorf("data = %#v, want id 7 and created false", reply.Data)
	}

	sink.fail(errors.New("disk I/O error"))
	ext.ask("req-t2", MethodTaskAdd, taskParams(nil))
	if reply := ext.settled(); reply.OK || reply.Code != CodeInternal {
		t.Errorf("a plain sink error: ok %v code %q, want internal", reply.OK, reply.Code)
	}
}
