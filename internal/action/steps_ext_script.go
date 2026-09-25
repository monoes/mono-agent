package action

// In-page steps: page_script, http_fetch_in_page, download — plus evalJS,
// the driver-neutral "run this expression in the page and give me JSON"
// helper the other extended steps share.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/fsconfine"
)

// jsString renders s as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsJSON renders v as a JavaScript literal (JSON is valid JS).
func jsJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// evalJS evaluates the JavaScript expression expr (it may produce a Promise)
// in the page and returns its value decoded from JSON. The value crosses
// the driver as a JSON string, so every driver hands back the same Go
// shapes (map[string]interface{}, []interface{}, float64, string, bool, nil).
//
// Pages that can evaluate through CDP (the extension) use that — it
// bypasses the page CSP, which blocks the extension's plain eval on sites
// forbidding 'unsafe-eval'. Others use page.Eval with a function wrapper.
func (ae *ActionExecutor) evalJS(ctx context.Context, expr string, timeout time.Duration) (interface{}, error) {
	if ae.page == nil {
		return nil, fmt.Errorf("no page")
	}
	body := "(async () => JSON.stringify(await (" + expr + ")))()"
	type out struct {
		v   interface{}
		err error
	}
	ch := make(chan out, 1)
	go func() {
		if ev, ok := ae.page.(cdpEvaluator); ok {
			v, err := ev.EvalCDP(body)
			ch <- out{v, err}
			return
		}
		res, err := ae.page.Timeout(timeout).Eval("() => " + body)
		if err != nil || res == nil {
			ch <- out{nil, err}
			return
		}
		ch <- out{res.Str(), nil}
	}()
	var r out
	select {
	case r = <-ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("page evaluation timed out after %s", timeout)
	}
	if r.err != nil {
		return nil, r.err
	}
	s, ok := r.v.(string)
	if !ok {
		// Not a JSON string (a test double, or an old driver): take as is.
		return r.v, nil
	}
	if s == "" || s == "undefined" {
		return nil, nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("page returned non-JSON result: %w", err)
	}
	return v, nil
}

// extSafeStop records a safe-mode stop before step (a step type that is a
// side effect by nature: page_script, download, a non-GET fetch, a call to a
// writing action) and returns the result that unwinds the run.
func (ae *ActionExecutor) extSafeStop(step StepDef) (*StepResult, error) {
	err := ae.stopBeforeSideEffect(step)
	return &StepResult{Success: false, Abort: true, StepID: step.ID, Error: err}, nil
}

func scriptsRefused(p PackageContext) string {
	id := ""
	if p != nil {
		id = p.ID()
	}
	return fmt.Sprintf("scripts are not allowed for automation %q (allow with: monoagentcli automation trust %s --scripts)", id, id)
}

// pageError reports a {"__error": "..."} object a page snippet returned.
func pageError(v interface{}) error {
	if m, ok := v.(map[string]interface{}); ok {
		if e, ok := m["__error"].(string); ok {
			return fmt.Errorf("%s", e)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// page_script
// ---------------------------------------------------------------------------

func (ae *ActionExecutor) stepPageScript(ctx context.Context, step StepDef) (*StepResult, error) {
	if ae.pkg == nil {
		return extFail(step, "page_script needs an automation package")
	}
	if !ae.scriptsAllowed() {
		return extFail(step, "%s", scriptsRefused(ae.pkg))
	}
	if ae.safeMode {
		return ae.extSafeStop(step)
	}
	name := ae.resolver.Resolve(step.Script)
	if name == "" {
		return extFail(step, "no script named")
	}
	src, err := ae.pkg.Script(name)
	if err != nil {
		return extFail(step, "script %q: %w", name, err)
	}
	if strings.TrimSpace(src) == "" {
		return extFail(step, "script %q is empty", name)
	}
	args, err := jsJSON(ae.extResolveInputs(step.Inputs))
	if err != nil {
		return extFail(step, "inputs are not JSON-serialisable: %w", err)
	}
	v, err := ae.evalJS(ctx, "(function(args){\n"+src+"\n})("+args+")", stepTimeout(step, 30))
	if err != nil {
		return extFail(step, "script %q: %w", name, err)
	}
	return ae.extStore(step, v), nil
}

// ---------------------------------------------------------------------------
// http_fetch_in_page
// ---------------------------------------------------------------------------

// maxFetchBody caps the response text http_fetch_in_page returns.
const maxFetchBody = 10 << 20

func (ae *ActionExecutor) stepHTTPFetchInPage(ctx context.Context, step StepDef) (*StepResult, error) {
	// A fetch with the page's session is a script capability (§8).
	if !ae.scriptsAllowed() {
		return extFail(step, "%s", scriptsRefused(ae.pkg))
	}
	target, err := ae.extAbsURL(step.URL)
	if err != nil {
		return extFail(step, "%w", err)
	}
	if err := ae.CheckURLAllowed(target); err != nil {
		return extFail(step, "%w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(step.Method))
	if method == "" {
		method = "GET"
	}
	readOnly := method == "GET" || method == "HEAD"
	if ae.safeMode && !readOnly {
		return ae.extSafeStop(step)
	}
	inputs := ae.extResolveInputs(step.Inputs)
	headers := map[string]string{}
	if h, ok := inputs["headers"].(map[string]interface{}); ok {
		for k, v := range h {
			headers[k] = fmt.Sprintf("%v", v)
		}
	}
	var body interface{}
	switch b := inputs["body"].(type) {
	case nil:
	case string:
		if b != "" {
			body = b
		}
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			return extFail(step, "body is not JSON-serialisable: %w", err)
		}
		body = string(enc)
		if !hasHeader(headers, "content-type") {
			headers["Content-Type"] = "application/json"
		}
	}
	// Redirects (contract §8, M4): page JS cannot see a manual redirect's
	// Location (the browser returns an opaque response), so hops cannot be
	// followed and checked one by one from Go. Instead:
	//   - a bodyless GET/HEAD with no custom headers follows redirects; its
	//     intermediate hops are not individually checked (they receive only
	//     their own host's cookies), and the final URL must be allowed;
	//   - a request with a body or headers uses redirect:'manual' and a
	//     redirect fails the step, so they are never re-sent elsewhere.
	redirect := "manual"
	if readOnly && body == nil && len(headers) == 0 {
		redirect = "follow"
	}
	opts, _ := jsJSON(map[string]interface{}{"method": method, "headers": headers, "body": body, "credentials": "include", "redirect": redirect})
	expr := fmt.Sprintf(`(async () => {
  const o = %s; if (o.body === null) delete o.body;
  const r = await fetch(%s, o);
  if (r.type === 'opaqueredirect') return {__error: 'the server redirected; redirects are not followed for requests with a body or headers — use the final URL'};
  const headers = {}; r.headers.forEach((v, k) => { headers[k] = v; });
  let text = await r.text();
  if (text.length > %d) return {__error: 'response larger than %d bytes'};
  let body = text;
  if (/json/i.test(r.headers.get('content-type') || '')) { try { body = JSON.parse(text); } catch (e) {} }
  return {status: r.status, headers, body, url: r.url};
})()`, opts, jsString(target), maxFetchBody, maxFetchBody)
	v, err := ae.evalJS(ctx, expr, stepTimeout(step, 30))
	if err != nil {
		return extFail(step, "fetch %s: %w", target, err)
	}
	if err := pageError(v); err != nil {
		return extFail(step, "fetch %s: %w", target, err)
	}
	res, ok := v.(map[string]interface{})
	if !ok {
		return extFail(step, "fetch %s: unexpected result %T", target, v)
	}
	// A redirect may have left the allowlist; refuse what came back.
	if final, _ := res["url"].(string); final != "" {
		if err := ae.CheckURLAllowed(final); err != nil {
			return extFail(step, "redirected: %w", err)
		}
	}
	delete(res, "url")
	return ae.extStore(step, res), nil
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// extAbsURL resolves a step URL against the current page.
func (ae *ActionExecutor) extAbsURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("no url")
	}
	abs := ae.resolveRelativeURL(raw)
	u, err := url.Parse(abs)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("url %q is not an absolute http(s) URL", abs)
	}
	return abs, nil
}

// ---------------------------------------------------------------------------
// download
// ---------------------------------------------------------------------------

// maxDownloadBytes caps one download.
const maxDownloadBytes = 25 << 20

func (ae *ActionExecutor) stepDownload(ctx context.Context, step StepDef) (*StepResult, error) {
	if !ae.DownloadsAllowed() {
		return extFail(step, "downloads are not permitted for this automation")
	}
	if ae.safeMode {
		return ae.extSafeStop(step)
	}
	raw := step.URL
	if raw == "" {
		if s, ok := step.Value.(string); ok {
			raw = s
		}
	}
	target, err := ae.extAbsURL(raw)
	if err != nil {
		return extFail(step, "%w", err)
	}
	if err := ae.CheckURLAllowed(target); err != nil {
		return extFail(step, "%w", err)
	}
	dir, err := ae.downloadDir(ctx)
	if err != nil {
		return extFail(step, "%w", err)
	}

	expr := fmt.Sprintf(`(async () => {
  const r = await fetch(%s, {credentials: 'include', redirect: 'follow'}); // bodyless GET: see the redirect note in stepHTTPFetchInPage
  if (!r.ok) return {__error: 'HTTP ' + r.status};
  const buf = await r.arrayBuffer();
  if (buf.byteLength > %d) return {__error: 'file larger than %d bytes'};
  const bytes = new Uint8Array(buf); let bin = '';
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return {data: btoa(bin), type: r.headers.get('content-type') || '', disposition: r.headers.get('content-disposition') || '', url: r.url};
})()`, jsString(target), maxDownloadBytes, maxDownloadBytes)
	v, err := ae.evalJS(ctx, expr, stepTimeout(step, 120))
	if err != nil {
		return extFail(step, "download %s: %w", target, err)
	}
	if err := pageError(v); err != nil {
		return extFail(step, "download %s: %w", target, err)
	}
	res, _ := v.(map[string]interface{})
	b64, _ := res["data"].(string)
	if res == nil || res["data"] == nil {
		return extFail(step, "download %s: page returned no data", target)
	}
	if final, _ := res["url"].(string); final != "" {
		if err := ae.CheckURLAllowed(final); err != nil {
			return extFail(step, "redirected: %w", err)
		}
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return extFail(step, "download %s: bad data: %w", target, err)
	}
	if len(data) > maxDownloadBytes {
		return extFail(step, "download %s: file larger than %d bytes", target, maxDownloadBytes)
	}

	name := ae.resolver.Resolve(step.Text)
	if name == "" {
		disp, _ := res["disposition"].(string)
		name = dispositionFilename(disp)
	}
	if name == "" {
		if u, err := url.Parse(target); err == nil {
			name = filepath.Base(u.Path)
		}
	}
	path, err := writeDownload(dir, name, data)
	if err != nil {
		return extFail(step, "%w", err)
	}
	ctype, _ := res["type"].(string)
	if v := extOutputVar(step); v != "" {
		ae.execCtx.SetVariable(v, path)
	}
	return &StepResult{Success: true, StepID: step.ID, Data: map[string]interface{}{"path": path, "size": len(data), "contentType": ctype}}, nil
}

// downloadDir is where a download lands: the run's confinement root (a
// role's workdir) when there is one, else ~/.monoagent/downloads/<automation>.
func (ae *ActionExecutor) downloadDir(ctx context.Context) (string, error) {
	sub := "actions"
	if ae.pkg != nil && ae.pkg.ID() != "" {
		sub = ae.pkg.ID()
	} else if ae.action != nil && ae.action.TargetPlatform != "" {
		sub = ae.action.TargetPlatform
	}
	sub = safeFileName(sub)
	if root, confined := fsconfine.Root(ctx); confined {
		if root == "" {
			return "", fmt.Errorf("run is confined to an invalid workdir; refusing to download")
		}
		return filepath.Join(root, "downloads", sub), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, ".monoagent", "downloads", sub), nil
}

var dispositionRe = regexp.MustCompile(`(?i)filename\*?=(?:UTF-8'')?"?([^";]+)"?`)

func dispositionFilename(d string) string {
	m := dispositionRe.FindStringSubmatch(d)
	if m == nil {
		return ""
	}
	if s, err := url.PathUnescape(m[1]); err == nil {
		return s
	}
	return m[1]
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

// safeFileName reduces name to one harmless path element.
func safeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(unsafeNameRe.ReplaceAllString(name, "_"))
	name = strings.TrimLeft(name, ".")
	if name == "" || name == "_" {
		return "download"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

// writeDownload writes data to dir/name, never overwriting: a taken name
// gets a " (n)" suffix.
func writeDownload(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create download dir: %w", err)
	}
	name = safeFileName(name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, cand)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("write download: %w", err)
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(p)
			return "", fmt.Errorf("write download: %v", firstErr(werr, cerr))
		}
		return p, nil
	}
	return "", fmt.Errorf("write download: no free name for %q", name)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
