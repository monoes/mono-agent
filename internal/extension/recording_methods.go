package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/recording"
)

// The record.* request methods: the side panel's Review flow asks this
// process to list, analyze, verify and save recordings. None of the logic
// lives here — each method runs this binary's own `record … --json`
// (contracts §5) and hands back its JSON, so the GUI, the CLI and the side
// panel can never disagree about what a command does.
//
// The params come from a browser, so every one is validated before it can
// become an argv word: ids match a strict pattern and can never start with
// a dash, a draft directory must resolve inside ~/.monoagent/recording-drafts,
// flag values are passed as --flag=value so none can be read as a flag.

// Record request methods.
const (
	MethodRecordList    = "record.list"
	MethodRecordAnalyze = "record.analyze"
	MethodRecordVerify  = "record.verify"
	MethodRecordSave    = "record.save"
)

// Per-method deadlines: analyze runs an AI pass, verify replays a flow.
const (
	recordListTimeout    = 30 * time.Second
	recordAnalyzeTimeout = 5 * time.Minute
	recordVerifyTimeout  = 3 * time.Minute
	recordSaveTimeout    = time.Minute
)

// stderrTail bounds how much of a failing command's stderr is quoted back.
const stderrTail = 600

var (
	automationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)
	recordNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_ .-]{0,63}$`)
)

func registerRecordHandlers(s *Server) {
	s.HandleRequestWithTimeout(MethodRecordList, s.handleRecordList, recordListTimeout)
	s.HandleRequestWithTimeout(MethodRecordAnalyze, s.handleRecordAnalyze, recordAnalyzeTimeout)
	s.HandleRequestWithTimeout(MethodRecordVerify, s.handleRecordVerify, recordVerifyTimeout)
	s.HandleRequestWithTimeout(MethodRecordSave, s.handleRecordSave, recordSaveTimeout)
}

// SetRecordRunner replaces the runner the record.* methods exec through.
// Tests call it; production uses this binary.
func (s *Server) SetRecordRunner(r Runner) {
	s.recMu.Lock()
	s.recRunner = r
	s.recMu.Unlock()
}

func (s *Server) recordRunner() Runner {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.recRunner == nil {
		s.recRunner = selfRunner{}
	}
	return s.recRunner
}

func (s *Server) handleRecordList(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
	args, err := recordListArgs(req)
	if err != nil {
		return nil, err
	}
	return runRecordJSON(ctx, s.recordRunner(), args)
}

func (s *Server) handleRecordAnalyze(ctx context.Context, req *Request, progress ProgressFunc) (any, error) {
	args, err := recordAnalyzeArgs(req)
	if err != nil {
		return nil, err
	}
	progress("analyzing", "")
	return runRecordJSON(ctx, s.recordRunner(), args)
}

func (s *Server) handleRecordVerify(ctx context.Context, req *Request, progress ProgressFunc) (any, error) {
	args, inputs, err := recordVerifyArgs(req)
	if err != nil {
		return nil, err
	}
	if len(inputs) > 0 {
		// Inputs are usually secrets: never on argv (/proc/*/cmdline is
		// readable by every local process), only in a private file that
		// is gone once the command returns.
		path, cleanup, err := writeVerifyInputs(inputs)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		args = append(args[:len(args)-1:len(args)-1], "--inputs-file="+path, "--json")
	}
	progress("verifying", "")
	return runRecordJSON(ctx, s.recordRunner(), args)
}

func (s *Server) handleRecordSave(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
	args, err := recordSaveArgs(req)
	if err != nil {
		return nil, err
	}
	return runRecordJSON(ctx, s.recordRunner(), args)
}

// badParam is the error for a param that failed validation.
func badParam(format string, a ...any) error {
	return &RequestError{Code: CodeBadParams, Err: fmt.Errorf(format, a...)}
}

// CodeBadParams is a request refused because a param failed validation.
const CodeBadParams = "bad_params"

// profileArgs returns the leading --profile flag when the request names a
// usable profile.
func profileArgs(req *Request) ([]string, error) {
	p := req.String("profile")
	if p == "" {
		return nil, nil
	}
	if !profiledir.ValidProfileID(p) || !safeArgValue(p) || strings.ContainsAny(p, "=") {
		return nil, badParam("invalid profile %q", firstLine(p))
	}
	return []string{"--profile=" + p}, nil
}

func recordListArgs(req *Request) ([]string, error) {
	args, err := profileArgs(req)
	if err != nil {
		return nil, err
	}
	return append(args, "record", "list", "--json"), nil
}

func recordAnalyzeArgs(req *Request) ([]string, error) {
	args, err := profileArgs(req)
	if err != nil {
		return nil, err
	}
	id := req.String("recordingId")
	if !recording.ValidID(id) {
		return nil, badParam("invalid recordingId %q", firstLine(id))
	}
	args = append(args, "record", "analyze", id)
	if a := req.String("automation"); a != "" {
		if !automationIDPattern.MatchString(a) {
			return nil, badParam("invalid automation id %q", firstLine(a))
		}
		args = append(args, "--automation="+a)
	}
	return append(args, "--json"), nil
}

// recordVerifyArgs builds the verify argv (ending in --json) and returns
// the validated inputs separately: they travel in a file, never on argv.
func recordVerifyArgs(req *Request) ([]string, map[string]string, error) {
	args, err := profileArgs(req)
	if err != nil {
		return nil, nil, err
	}
	dir, err := recording.ResolveDraftDir(req.String("draftDir"))
	if err != nil {
		return nil, nil, badParam("%v", err)
	}
	args = append(args, "record", "verify", dir)
	if full, _ := req.Params["full"].(bool); full {
		args = append(args, "--full")
	}
	inputs, err := verifyInputs(req.Params["inputs"])
	if err != nil {
		return nil, nil, err
	}
	return append(args, "--json"), inputs, nil
}

// Bounds on record.verify's inputs: one replay's worth of form fields.
const (
	maxVerifyInputs     = 64
	maxVerifyInputBytes = 8 << 10
)

// inputNamePattern matches recordanalyze.ValidInputName, so a name the
// verify command would refuse is refused here with bad_params instead.
var inputNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

// verifyInputs validates record.verify's {"name": "value"} inputs. Values
// may be one-off secrets for this replay: they are never logged or put on
// argv (see writeVerifyInputs).
func verifyInputs(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, badParam("inputs must be an object of name → string")
	}
	if len(m) > maxVerifyInputs {
		return nil, badParam("too many inputs (max %d)", maxVerifyInputs)
	}
	out := make(map[string]string, len(m))
	for name, v := range m {
		if !inputNamePattern.MatchString(name) {
			return nil, badParam("invalid input name %q", firstLine(name))
		}
		val, ok := v.(string)
		if !ok {
			return nil, badParam("input %s must be a string", name)
		}
		if len(val) > maxVerifyInputBytes || strings.ContainsRune(val, 0) {
			return nil, badParam("input %s value is too long or contains NUL", name)
		}
		out[name] = val
	}
	return out, nil
}

// verifyInputsDir is where inputs files are written: ~/.monoagent/tmp, on
// the user's own disk rather than a shared /tmp.
func verifyInputsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".monoagent", "tmp"), nil
}

// writeVerifyInputs writes inputs to a 0600 file in a 0700 directory for
// `record verify --inputs-file`, and returns a cleanup that removes it.
func writeVerifyInputs(inputs map[string]string) (string, func(), error) {
	dir, err := verifyInputsDir()
	if err != nil {
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	blob, err := json.Marshal(inputs)
	if err != nil {
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	f, err := os.CreateTemp(dir, "verify-inputs-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	if _, err := f.Write(blob); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("inputs file: %w", err)
	}
	return f.Name(), cleanup, nil
}

// redactArgs renders an argv for an error message with any --input values
// masked. The bridge itself passes inputs by file; this guards any argv
// that still carries one.
func redactArgs(args []string) string {
	shown := make([]string, len(args))
	for i, a := range args {
		if rest, ok := strings.CutPrefix(a, "--input="); ok {
			name, _, _ := strings.Cut(rest, "=")
			a = "--input=" + name + "=•••"
		}
		shown[i] = a
	}
	return strings.Join(shown, " ")
}

func recordSaveArgs(req *Request) ([]string, error) {
	args, err := profileArgs(req)
	if err != nil {
		return nil, err
	}
	dir, err := recording.ResolveDraftDir(req.String("draftDir"))
	if err != nil {
		return nil, badParam("%v", err)
	}
	args = append(args, "record", "save", dir)
	switch as := req.String("saveAs"); as {
	case "":
	case "action", "fragment", "workflow":
		args = append(args, "--as="+as)
	default:
		return nil, badParam("saveAs must be action, fragment or workflow, not %q", firstLine(as))
	}
	automation, newID := req.String("automation"), req.String("new")
	if automation != "" && newID != "" {
		return nil, badParam("give automation or new, not both")
	}
	for flag, v := range map[string]string{"automation": automation, "new": newID} {
		if v != "" && !automationIDPattern.MatchString(v) {
			return nil, badParam("invalid %s id %q", flag, firstLine(v))
		}
	}
	if automation != "" {
		args = append(args, "--automation="+automation)
	}
	if newID != "" {
		args = append(args, "--new="+newID)
	}
	if name := req.String("name"); name != "" {
		if !recordNamePattern.MatchString(name) {
			return nil, badParam("invalid name %q", firstLine(name))
		}
		args = append(args, "--name="+name)
	}
	// force: the person confirmed "Save anyway" on a draft whose lint has
	// errors. Only a real boolean counts.
	if raw, ok := req.Params["force"]; ok && raw != nil {
		force, isBool := raw.(bool)
		if !isBool {
			return nil, badParam("force must be a boolean")
		}
		if force {
			args = append(args, "--force")
		}
	}
	return append(args, "--json"), nil
}

// runRecordJSON runs one `record …` command and decodes its stdout.
//
// A command that fails still answers with its stdout when that is a report:
// `record verify` exits 1 on a failed replay and prints the step results as
// its whole output (reportedError), and the side panel needs those steps
// far more than it needs "verify failed". So on a non-zero exit:
//
//   - a JSON object with an "error" field is the failure (contracts §5), and
//     its message wins over stderr since it is the one written for a person;
//   - any other JSON object is a report, returned as the result;
//   - missing or non-JSON stdout is the run's own error.
func runRecordJSON(ctx context.Context, r Runner, args []string) (any, error) {
	out, runErr := r.Run(ctx, args...)
	trimmed := bytes.TrimSpace(out)
	var obj map[string]any
	isObject := len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &obj) == nil
	if runErr != nil {
		if !isObject {
			return nil, runErr
		}
		if msg, ok := obj["error"].(string); ok && msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		if _, hasErr := obj["error"]; hasErr {
			return nil, runErr
		}
		return obj, nil
	}
	if isObject {
		return obj, nil
	}
	var data any
	if err := json.Unmarshal(trimmed, &data); err != nil {
		return nil, fmt.Errorf("monoagentcli %s: output is not JSON", redactArgs(args))
	}
	return data, nil
}

// selfRunner runs this very binary. Only a monoagentcli can answer
// `record …`; any other host of the server (cmd/inspect, a test binary)
// reports the methods as unavailable instead of exec'ing something
// arbitrary.
type selfRunner struct{}

func (selfRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, Unavailable("cannot locate monoagentcli: %v", err)
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(bin)), ".exe")
	if !strings.HasPrefix(base, "monoagentcli") {
		return nil, Unavailable("record methods need the monoagentcli bridge (running as %s)", filepath.Base(bin))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = monomindWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.Bytes(), &RequestError{Code: CodeTimeout, Err: fmt.Errorf("monoagentcli %s: %w", redactArgs(args), ctx.Err())}
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > stderrTail {
			detail = "…" + detail[len(detail)-stderrTail:]
		}
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("monoagentcli %s: %s", redactArgs(args), detail)
	}
	return stdout.Bytes(), nil
}
