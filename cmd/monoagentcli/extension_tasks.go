package main

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/tasks"
)

// The task board's half of the extension bridge (task board spec 11.2): a
// task.add from the MonoAgent Bridge extension becomes an Inbox task in the
// profile it names. newExtensionServer installs it on every bridge this
// binary builds, so whichever process holds the bridge answers: the daemon,
// `extension serve` or a workflow run.
//
// The words go through internal/tasks like every other surface's: cleaned
// (control and hidden characters, invalid UTF-8), cut to the limits, the
// address checked. The actor is always the Chrome capture, whatever
// environment this process inherited: a daemon started from an agent's shell
// must not turn the person's browser captures into rate-limited agent tasks,
// and a capture never reaches Ready.

// chromeCapture is who every task.add acts as.
var chromeCapture = tasks.Actor{Kind: tasks.Capture, Name: tasks.SourceChrome}

// boardSink files task.add requests on the board in the database at path. It
// opens the database per request, as the profile source does: captures are
// rare, and openProfileDB never creates a database a browser asked about.
type boardSink struct{ path string }

// extensionTaskSink is the sink newExtensionServer installs.
func extensionTaskSink(path string) extension.TaskSink { return boardSink{path: path} }

// AddCaptured implements extension.TaskSink.
func (b boardSink) AddCaptured(ctx context.Context, t extension.CapturedTask) (extension.TaskAdded, error) {
	db, err := openProfileDB(b.path)
	if err != nil {
		// A fixed message: openProfileDB's names the database's path, and the
		// extension may show a reason in the page as a toast, where the
		// page's own script can read it.
		return extension.TaskAdded{}, extension.Unavailable("MonoAgent has no database yet: start MonoAgent or run any monoagentcli command")
	}
	defer db.Close()
	// `extension serve` never migrates, so a database from before the board
	// has no tasks table until some command has run. The task waits in the
	// extension's outbox meanwhile; it is not refused, and this does not
	// migrate: a question from a browser does not change the database.
	var tables int
	err = db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'tasks'`).Scan(&tables)
	if err != nil {
		return extension.TaskAdded{}, extension.Unavailable("MonoAgent could not read its database; the task is kept and sent again")
	}
	if tables == 0 {
		return extension.TaskAdded{}, extension.Unavailable("the monoagent database has no task board yet: run any monoagentcli command once to upgrade it")
	}
	task, created, err := addCaptured(ctx, tasks.NewStore(db.DB), t)
	if err != nil {
		return extension.TaskAdded{}, boardSinkErr(err)
	}
	return extension.TaskAdded{ID: task.ID, Created: created}, nil
}

// capturedInput applies spec 4.6 to each kind: a selection or a note is text
// whose first line becomes the title; a page is its title, with no notes
// (addCaptured names a page by its address when the board refuses the title).
func capturedInput(t extension.CapturedTask) tasks.AddInput {
	in := tasks.AddInput{
		SourceKind:  tasks.SourceChrome,
		SourceURL:   t.URL,
		SourceTitle: t.Title,
		SourceApp:   t.Origin,
		ClientID:    t.ClientID,
	}
	if t.Kind != extension.TaskKindPage {
		in.Text = t.Text
		return in
	}
	in.Title = t.Title
	return in
}

// addCaptured files t on the board. A page is named by its title; when the
// board refuses that title (blank, or nothing visible once the board has
// cleaned it: a bidi control and BEL are not blank to a white-space test) it is
// named by its address, asked once more, so that no page is lost for a title
// its own script wrote. The board alone says what a usable title is: a copy of
// its cleaning here could drift from it. A page sends no text or notes and the
// actor and the source are fixed, so a bad title is the only invalid_input a
// second try can cure; any other (a deleted profile) is refused again, and that
// second refusal is the one reported. With no address to fall back on, the
// first refusal is the one reported.
func addCaptured(ctx context.Context, store *tasks.Store, t extension.CapturedTask) (tasks.Task, bool, error) {
	in := capturedInput(t)
	task, created, err := store.Add(ctx, t.ProfileID, in, chromeCapture)
	if t.Kind != extension.TaskKindPage || !errors.Is(err, tasks.ErrInvalid) {
		return task, created, err
	}
	address := addressOf(t.URL)
	if address == "" {
		return task, created, err
	}
	in.Title = address
	return store.Add(ctx, t.ProfileID, in, chromeCapture)
}

// addressOf is a page address fit to be a title: parsed and without the
// user-info an address can carry ("https://user:password@host/"); "" when it
// is not an absolute address.
func addressOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.User = nil
	return u.String()
}

// boardSinkErr gives a board refusal the code the extension's outbox acts on:
// invalid_input is dropped and reported, limit waits. Anything else is
// internal and waits too, under a fixed message: the extension may show a
// reason in the page, and a database error can name files.
func boardSinkErr(err error) error {
	switch {
	case errors.Is(err, tasks.ErrInvalid):
		return &extension.RequestError{Code: extension.CodeInvalidInput, Err: err}
	case errors.Is(err, tasks.ErrLimit):
		return &extension.RequestError{Code: extension.CodeLimit, Err: err}
	}
	return &extension.RequestError{Code: extension.CodeInternal, Err: errors.New("MonoAgent could not add the task; it is kept and tried again")}
}
