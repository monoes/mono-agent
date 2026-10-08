package extension

import (
	"context"
	"errors"
	"fmt"
)

// task.add: "put this on my task board" (task board spec section 11.2).
//
// The extension never adds one task twice by accident: its outbox
// (chrome-extension/task_outbox.js) keeps each task under a client id made
// when the task was queued and sends it again until a reply settles it, and
// the board answers a client id it has seen with the task it already made.
// Every task lands in the Inbox of the profile the request names. There is
// no task outside a profile, so an empty or unknown profile is refused,
// never filed somewhere else.
//
// The board lives in the host's database, which this package never opens:
// the host hands over a TaskSink (cmd/monoagentcli/extension_tasks.go). A
// host with none does not advertise the method, which is how a newer
// extension tells an older daemon apart and keeps the task queued. Nothing
// here logs a payload: a task is text from a web page.
//
//	extension -> Go  {"kind":"request","id":"req-...","method":"task.add",
//	                  "params":{"client_id":"t-...","profile":"p-work","kind":"selection",
//	                            "text":"...","url":"https://...","title":"..."}}
//	Go -> extension  {"kind":"reply","id":"req-...","ok":true,"data":{"id":12,"created":true}}

// MethodTaskAdd adds a captured task to the Inbox of a profile's board.
const MethodTaskAdd = "task.add"

// Codes task.add answers with beyond the channel's own. The extension's
// outbox drops an entry refused with CodeInvalidInput (asking again would be
// refused again) and keeps one refused with CodeLimit (the board is full
// until the person archives something).
const (
	CodeInvalidInput = "invalid_input"
	CodeLimit        = "limit"
)

// The kinds of task the extension sends.
const (
	TaskKindSelection = "selection" // text selected on a page
	TaskKindPage      = "page"      // a whole page: its title and address
	TaskKindNote      = "note"      // typed in the side panel, about no page
)

// CapturedTask is a checked task.add request, as the sink receives it.
type CapturedTask struct {
	ProfileID string
	ClientID  string
	Kind      string
	Text      string
	URL       string
	Title     string
	// Origin names the browser that sent it: its label, else "Chrome". It
	// comes from the connection, never from the request.
	Origin string
}

// TaskAdded is task.add's reply: the task, and whether this request made it
// (false: the client id was seen before and this is that task).
type TaskAdded struct {
	ID      int64 `json:"id"`
	Created bool  `json:"created"`
}

// TaskSink files captured tasks on a board. An error that must reach the
// extension with its code is a *RequestError (CodeInvalidInput, CodeLimit,
// CodeUnavailable); any other error arrives as CodeInternal.
type TaskSink interface {
	AddCaptured(ctx context.Context, t CapturedTask) (TaskAdded, error)
}

// SetTaskSink installs the sink behind task.add and registers the method with
// it; nil takes the method away again. As with SetProfileSource, "there is a
// sink" and "ping advertises task.add" are one fact, not two that can drift.
func (s *Server) SetTaskSink(sink TaskSink) {
	if sink == nil {
		s.handlerMu.Lock()
		delete(s.handlers, MethodTaskAdd)
		s.handlerMu.Unlock()
		return
	}
	registerTaskHandlers(s, sink)
}

// registerTaskHandlers installs task.add over sink.
func registerTaskHandlers(s *Server, sink TaskSink) {
	s.HandleRequest(MethodTaskAdd, func(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
		t, err := capturedTaskOf(req)
		if err != nil {
			return nil, err
		}
		added, err := sink.AddCaptured(ctx, t)
		if err != nil {
			return nil, err
		}
		return added, nil
	})
}

// capturedTaskOf reads and checks a request. The words are cleaned and cut
// by the board (internal/tasks, spec 4.6), which every surface goes through;
// this refuses only what no board could file.
func capturedTaskOf(req *Request) (CapturedTask, error) {
	t := CapturedTask{
		ProfileID: req.String("profile"),
		ClientID:  req.String("client_id"),
		Kind:      req.String("kind"),
		Text:      req.String("text"),
		URL:       req.String("url"),
		Title:     req.String("title"),
		Origin:    browserApp(req.Origin),
	}
	switch {
	case t.ProfileID == "":
		return CapturedTask{}, refusal("task.add needs a profile: a task always sits in one profile")
	case t.ClientID == "":
		return CapturedTask{}, refusal("task.add needs a client_id, so that a retry cannot add a task twice")
	}
	switch t.Kind {
	case TaskKindSelection, TaskKindPage:
	case TaskKindNote:
		// Typed in the side panel: it is about no page, whatever the request says.
		t.URL, t.Title = "", ""
	default:
		return CapturedTask{}, refusal(fmt.Sprintf("task.add kind %q is not selection, page or note", firstLine(t.Kind)))
	}
	return t, nil
}

// refusal is an invalid_input error: the outbox drops the entry and says why.
func refusal(msg string) error {
	return &RequestError{Code: CodeInvalidInput, Err: errors.New(msg)}
}

// browserApp is the app a task records it came from: the label the person
// gave this browser, else the browser the extension is made for.
func browserApp(o ConnInfo) string {
	if o.Label != "" {
		return o.Label
	}
	return "Chrome"
}
