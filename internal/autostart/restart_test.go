package autostart

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeInstaller is a service manager that runs nothing.
type fakeInstaller struct {
	installed    bool
	where        string
	restartErr   error
	statusCalls  int
	restartCalls int
}

func (f *fakeInstaller) Install(context.Context) (Result, error) { return Result{}, nil }
func (f *fakeInstaller) Uninstall(context.Context) error         { return nil }
func (f *fakeInstaller) Start(context.Context) error             { return nil }
func (f *fakeInstaller) Status(context.Context) (bool, string) {
	f.statusCalls++
	return f.installed, f.where
}
func (f *fakeInstaller) Restart(context.Context) error { f.restartCalls++; return f.restartErr }

func TestRestartRegisteredRestartsTheRegisteredService(t *testing.T) {
	f := &fakeInstaller{installed: true, where: "/x/unit"}
	r, err := RestartRegistered(context.Background(), f)
	if err != nil || !r.Restarted || r.Via != ServiceManager() || r.Via == "" {
		t.Fatalf("RestartRegistered = %+v, %v", r, err)
	}
	if f.statusCalls != 1 || f.restartCalls != 1 {
		t.Errorf("status asked %d times, restarted %d times", f.statusCalls, f.restartCalls)
	}
}

// Nothing registered is nothing anything can restart: the error says what to do, and Restart
// is never called.
func TestRestartRegisteredWithNothingRegisteredRestartsNothing(t *testing.T) {
	for _, where := range []string{"", "/x/unit exists but the service manager has not loaded it"} {
		f := &fakeInstaller{installed: false, where: where}
		r, err := RestartRegistered(context.Background(), f)
		var nr *NotRegisteredError
		if !errors.As(err, &nr) || r.Restarted || r.Via != "" || f.restartCalls != 0 {
			t.Fatalf("where %q: %+v, %v, restarted %d times", where, r, err, f.restartCalls)
		}
		msg := err.Error()
		for _, want := range []string{"not registered for auto-start", "stop", "start `monoagentcli daemon` again", "daemon install"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the message %q does not say %q", msg, want)
			}
		}
		if where != "" && !strings.Contains(msg, where) {
			t.Errorf("the message should carry what the service manager said: %q", msg)
		}
		if nr.Detail != where {
			t.Errorf("Detail = %q", nr.Detail)
		}
	}
}

func TestRestartRegisteredPassesAFailureOn(t *testing.T) {
	boom := errors.New("launchctl kickstart: boom")
	r, err := RestartRegistered(context.Background(), &fakeInstaller{installed: true, restartErr: boom})
	if !errors.Is(err, boom) || r.Restarted || r.Via != "" {
		t.Errorf("%+v, %v", r, err)
	}
	var nr *NotRegisteredError
	if errors.As(err, &nr) {
		t.Error("a failed restart is not a missing registration")
	}
}

func TestServiceManagerNamesTheOneOfThisSystem(t *testing.T) {
	want := map[string]string{"darwin": "launchd", "linux": "systemd", "windows": "schtasks"}[runtime.GOOS]
	if got := ServiceManager(); got != want || strings.ContainsAny(got, " \t") {
		t.Errorf("ServiceManager() = %q on %s, want the token %q", got, runtime.GOOS, want)
	}
}

// What a restart does where the service manager has no restart of its own (the Windows Scheduled
// Task): end the task, wait for the daemon to let go of its lock, run it. A run that raced the
// old process would start a daemon that cannot take the lock and exits, and the task has no
// keep-alive.
func TestEndThenRun(t *testing.T) {
	type call struct{ args string }
	var order []string
	runner := func(fail map[string]error) func(context.Context, ...string) ([]byte, error) {
		return func(_ context.Context, args ...string) ([]byte, error) {
			order = append(order, strings.Join(args, " "))
			return []byte("out of " + args[0]), fail[args[0]]
		}
	}
	waited := func(err error) func(context.Context) error {
		return func(context.Context) error { order = append(order, "wait"); return err }
	}

	// end, wait, run: in that order, with the task's name.
	if err := endThenRun(context.Background(), runner(nil), waited(nil), "MyTask"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, " | "); got != "/end /tn MyTask | wait | /run /tn MyTask" {
		t.Errorf("order: %s", got)
	}

	// The task not running is not a failure of /end.
	order = nil
	if err := endThenRun(context.Background(), runner(map[string]error{"/end": errors.New("not running")}), waited(nil), "MyTask"); err != nil {
		t.Errorf("an /end that fails must not stop the restart: %v", err)
	}
	if got := strings.Join(order, " | "); got != "/end /tn MyTask | wait | /run /tn MyTask" {
		t.Errorf("order: %s", got)
	}

	// A daemon that did not stop: nothing is run, and the error says so.
	order = nil
	err := endThenRun(context.Background(), runner(nil), waited(errors.New("still running")), "MyTask")
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("a daemon that did not stop: %v", err)
	}
	if got := strings.Join(order, " | "); got != "/end /tn MyTask | wait" {
		t.Errorf("a daemon that did not stop must not be run again: %s", got)
	}

	// A /run that fails carries what schtasks said.
	order = nil
	err = endThenRun(context.Background(), runner(map[string]error{"/run": errors.New("exit status 1")}), waited(nil), "MyTask")
	if err == nil || !strings.Contains(err.Error(), "schtasks /run") || !strings.Contains(err.Error(), "out of /run") {
		t.Errorf("a failed /run: %v", err)
	}
}

func TestWaitForReturnsWhenTheConditionHolds(t *testing.T) {
	n := 0
	err := waitFor(context.Background(), func() bool { n++; return n >= 3 }, time.Second, time.Millisecond)
	if err != nil || n != 3 {
		t.Errorf("err %v after %d checks", err, n)
	}
	// Already true: no waiting at all.
	start := time.Now()
	if err := waitFor(context.Background(), func() bool { return true }, time.Hour, time.Hour); err != nil || time.Since(start) > time.Second {
		t.Errorf("a condition that already holds must return at once: %v after %v", err, time.Since(start))
	}
}

func TestWaitForGivesUpAtItsDeadlineAndWhenTheContextEnds(t *testing.T) {
	start := time.Now()
	err := waitFor(context.Background(), func() bool { return false }, 50*time.Millisecond, 5*time.Millisecond)
	if err == nil || time.Since(start) < 40*time.Millisecond || time.Since(start) > 2*time.Second {
		t.Errorf("a condition that never holds: %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start = time.Now()
	err = waitFor(ctx, func() bool { return false }, time.Hour, 5*time.Millisecond)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Errorf("a cancelled context: %v after %v", err, time.Since(start))
	}
}
