package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// libFixture is a fake monoes.me, a test home and a logged-out CLI.
type libFixture struct {
	t    *testing.T
	fake *libraryfake.Server
	home string
}

func newLibFixture(t *testing.T) *libFixture {
	t.Helper()
	keyring.MockInit()
	fake := libraryfake.New()
	t.Cleanup(fake.Close)
	t.Setenv("MONOES_BASE_URL", fake.URL)
	// The machine session is signed at the fake: its host, its signing key, and a
	// sealer of its own, so its refresh token depends on no key store (not even the
	// mock keyring above, which most tests re-make).
	account.SetHostForTest(t, fake.URL)
	account.SetSealerForTest(t, account.NewMemorySealer())
	libraryfake.TrustKey(t)
	// The browser: open the authorize URL and follow its redirect back to
	// the CLI's loopback listener.
	prev := openLoginURL
	openLoginURL = func(u string) error {
		go func() {
			if resp, err := http.Get(u); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { openLoginURL = prev })
	return &libFixture{t: t, fake: fake, home: t.TempDir()}
}

// noSeed makes the test home a fresh install: no built-in packages.
func noSeed(t *testing.T) {
	prev := automation.TestSeed
	automation.TestSeed = nil
	t.Cleanup(func() { automation.TestSeed = prev })
}

// run runs `monoagentcli --json <args>` and returns stdout and the error.
func (f *libFixture) run(args ...string) (string, error) {
	f.t.Helper()
	out, _, err := runCLI(f.t, f.home, append([]string{"--json"}, args...)...)
	return out, err
}

func (f *libFixture) must(v any, args ...string) {
	f.t.Helper()
	out, errOut, err := runCLI(f.t, f.home, append([]string{"--json"}, args...)...)
	if err != nil {
		f.t.Fatalf("%v: %v\nstdout: %s\nstderr: %s", args, err, out, errOut)
	}
	if v != nil {
		if err := json.Unmarshal([]byte(lastJSONObject([]byte(out))), v); err != nil {
			f.t.Fatalf("%v: not JSON: %v\n%s", args, err, out)
		}
	}
}

// login logs the default profile in, or another one: login("--profile", "x").
func (f *libFixture) login(profile ...string) {
	f.t.Helper()
	var st libStatus
	f.must(&st, append(profile, "library", "login")...)
	if !st.LoggedIn || st.User == nil || st.User.Username != "ada" {
		f.t.Fatalf("login status = %+v", st)
	}
}

// pack packs automations/<id> (optionally at another version) into .mpkg bytes.
func pack(t *testing.T, id, version string) []byte {
	t.Helper()
	src := filepath.Join("..", "..", "automations", id)
	if version != "" {
		dst := filepath.Join(t.TempDir(), id)
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		mf := filepath.Join(dst, "automation.json")
		var m map[string]any
		b, _ := os.ReadFile(mf)
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		m["version"] = version
		b, _ = json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(mf, b, 0o644); err != nil {
			t.Fatal(err)
		}
		src = dst
	}
	var buf bytes.Buffer
	if err := automation.Pack(src, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readRepo(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func automationRow(t *testing.T, f *libFixture, id string) *automation.InstalledInfo {
	t.Helper()
	var got struct {
		Automations []automation.InstalledInfo `json:"automations"`
	}
	f.must(&got, "automation", "list")
	for i := range got.Automations {
		if got.Automations[i].ID == id {
			return &got.Automations[i]
		}
	}
	return nil
}

func TestLibraryLoginStatusLogout(t *testing.T) {
	f := newLibFixture(t)
	var st libStatus
	f.must(&st, "library", "status")
	if st.LoggedIn || st.BaseURL != f.fake.URL {
		t.Fatalf("before login: %+v", st)
	}
	f.login()
	f.must(&st, "library", "status")
	if !st.LoggedIn || st.User.Email != "ada@example.com" || st.Method != "pkce" {
		t.Fatalf("status = %+v", st)
	}
	// The login lives in the profile's vault, never in plain config.
	var secrets []struct{ Name, URL string }
	f.must(&secrets, "secret", "list")
	found := false
	for _, s := range secrets {
		found = found || (s.Name == "monoes-library" && s.URL == f.fake.URL)
	}
	if !found {
		t.Fatalf("no vault entry: %+v", secrets)
	}
	// Another profile is not logged in.
	f.must(nil, "profile", "create", "other")
	f.must(&st, "--profile", "other", "library", "status")
	if st.LoggedIn {
		t.Fatal("login leaked into another profile")
	}
	f.must(nil, "library", "logout")
	f.must(&st, "library", "status")
	if st.LoggedIn {
		t.Fatalf("after logout: %+v", st)
	}
}

func TestLibraryEmailLogin(t *testing.T) {
	f := newLibFixture(t)
	var sent map[string]any
	f.must(&sent, "library", "login", "--email", "ada@example.com", "--send")
	if sent["code_sent"] != true {
		t.Fatalf("send = %v", sent)
	}
	out, err := f.run("library", "login", "--email", "ada@example.com", "--code", "999999")
	if err == nil || exitCodeFor(err) != 4 || !strings.Contains(out, "auth_or_connection") {
		t.Fatalf("wrong code: %v %s", err, out)
	}
	var st libStatus
	f.must(&st, "library", "login", "--email", "ada@example.com", "--code", "123456")
	if !st.LoggedIn || st.Method != "email" {
		t.Fatalf("status = %+v", st)
	}
}

func TestLibraryInstallAutomationTrust(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""),
		map[string]any{"automation_id": "hackernews"})
	f.fake.AddUser(&libraryfake.User{ID: "u-eve", Username: "eve", Name: "Eve", Email: "eve@example.com"})
	f.fake.Add("eve", "automation", "producthunt", "Product Hunt (community)", "public", "1.0.0", pack(t, "producthunt", ""), nil)
	// "official" visibility from anyone but monoes is not trusted.
	f.fake.Add("eve", "automation", "x", "X (fake official)", "official", "1.0.0", pack(t, "x", ""), nil)
	f.login()

	if automationRow(t, f, "hackernews") != nil {
		t.Fatal("a fresh install has hackernews")
	}
	var res libInstallResult
	f.must(&res, "library", "install", "automation", "hackernews")
	if !res.Installed || res.LocalID != "hackernews" || res.Trust != automation.TrustBuiltin || res.SHA256 == "" {
		t.Fatalf("official install = %+v", res)
	}
	row := automationRow(t, f, "hackernews")
	if row == nil || row.Source != automation.SourceMonoes || row.Trust != automation.TrustBuiltin ||
		row.Library == nil || !row.Library.Official || row.Library.SHA256 != res.SHA256 {
		t.Fatalf("hackernews row = %+v", row)
	}

	f.must(&res, "library", "install", "automations", "producthunt") // the plural the web shows
	if row := automationRow(t, f, "producthunt"); row == nil || row.Trust != automation.TrustImported || row.Library == nil || row.Library.Official {
		t.Fatalf("community row = %+v", row)
	}
	f.must(&res, "library", "install", "automation", "x")
	if row := automationRow(t, f, "x"); row == nil || row.Trust != automation.TrustImported {
		t.Fatalf("fake official row = %+v", row)
	}

	var inst struct {
		Items []struct{ Kind, LocalID string }
	}
	f.must(&inst, "library", "installed")
	if len(inst.Items) != 3 {
		t.Fatalf("installed = %+v", inst)
	}
}

func TestLibraryInstallRejectsBadSHA256(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""), nil)
	f.login()
	f.fake.TamperArtifact = true
	out, err := f.run("library", "install", "automation", "hackernews")
	if err == nil || exitCodeFor(err) != 3 || !strings.Contains(out, "sha256") {
		t.Fatalf("tampered install: %v %s", err, out)
	}
	if automationRow(t, f, "hackernews") != nil {
		t.Fatal("a tampered package was installed")
	}
}

func TestLibraryErrorsExitCodes(t *testing.T) {
	f := newLibFixture(t)
	if _, err := f.run("library", "list", "--kind", "bogus"); exitCodeFor(err) != 3 {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := f.run("library", "publish", "org", "none"); err == nil {
		t.Fatal("publish without login worked")
	}
	f.login()
	if _, err := f.run("library", "show", "workflow/nope"); exitCodeFor(err) != 2 {
		t.Fatalf("missing item: %v", err)
	}
}

// loginRequired asserts exit 4 with the "log in first" message, both as
// --json (with login_required) and as plain text.
func (f *libFixture) loginRequired(args ...string) {
	f.t.Helper()
	out, err := f.run(args...)
	var body struct {
		Error, Code   string
		LoginRequired bool `json:"login_required"`
	}
	if exitCodeFor(err) != 4 || json.Unmarshal([]byte(lastJSONObject([]byte(out))), &body) != nil ||
		body.Error != "Log in to monoes.me first: monoagentcli library login" || !body.LoginRequired || body.Code != "auth_or_connection" {
		f.t.Fatalf("%v: want exit 4 + log in first, got %v %s", args, err, out)
	}
	_, errOut, err := runCLI(f.t, f.home, args...)
	if exitCodeFor(err) != 4 || !strings.Contains(errOut+err.Error(), "Log in to monoes.me first: monoagentcli library login") {
		f.t.Fatalf("%v (text): %v %s", args, err, errOut)
	}
}

// Browsing and installing need a login, official items included: without
// one the CLI stops before asking monoes.me anything.
func TestLibraryReadsNeedALogin(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""),
		map[string]any{"automation_id": "hackernews"})
	for _, args := range [][]string{
		{"library", "list"},
		{"library", "list", "--scope", "official", "--kind", "automation"},
		{"library", "list", "--scope", "mine"},
		{"library", "show", "automation/hackernews"},
		{"library", "install", "automation", "hackernews"},
		{"library", "install", "automation", "hackernews", "--dry-run"},
		{"library", "update"},
		{"library", "update", "--dry-run"},
	} {
		f.loginRequired(args...)
	}
	for k, n := range f.fake.Requests {
		if strings.Contains(k, "/api/library/") && n > 0 {
			t.Fatalf("%s was called %d times without a login", k, n)
		}
	}
	// status and the local list still work logged out.
	var st libStatus
	f.must(&st, "library", "status")
	if st.LoggedIn {
		t.Fatalf("status = %+v", st)
	}
	f.must(nil, "library", "installed")
}

// A 401 on a read refreshes the login once and retries; a 401 the refresh
// cannot cure (the login was revoked) is "log in first", exit 4.
func TestLibraryRead401RefreshesThenAsksForLogin(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""),
		map[string]any{"automation_id": "hackernews"})
	f.login()

	f.fake.ExpireAccessTokens()
	var list libList
	f.must(&list, "library", "list", "--scope", "official")
	if list.Total != 1 || f.fake.Refreshes != 1 {
		t.Fatalf("list after expiry = %+v (refreshes %d)", list, f.fake.Refreshes)
	}
	var res libInstallResult
	f.must(&res, "library", "install", "automation", "hackernews")
	if !res.Installed {
		t.Fatalf("install = %+v", res)
	}

	f.fake.RevokeAll()
	before := f.fake.Requests["GET /api/library/items"]
	f.loginRequired("library", "list", "--scope", "official")
	if got := f.fake.Requests["GET /api/library/items"] - before; got != 1 { // the --json run only: its refresh was refused, which drops the login, so the text run asks nothing
		t.Fatalf("list requests = %d, want 1", got)
	}
	f.loginRequired("library", "show", "automation/hackernews")
	f.loginRequired("library", "update", "--dry-run")
	if f.fake.Refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", f.fake.Refreshes)
	}

	// Logging in again fixes it.
	f.login()
	f.must(&list, "library", "list", "--scope", "official")
	if list.Total != 1 || list.Items[0].Installed == nil {
		t.Fatalf("list after a new login = %+v", list)
	}
}
