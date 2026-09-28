package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/storage"
)

// newLibraryCmd is `library`: the monoes.me library of workflows, orgs and
// web automations — log in, browse, install and publish. The desktop app
// shells out to these commands (spec: monoes.me Library ↔ MonoAgent v1).
func newLibraryCmd(cfg *globalConfig) *cobra.Command {
	env := &libEnv{cfg: cfg}
	cmd := &cobra.Command{
		Use:   "library",
		Short: "Browse, install and publish workflows, orgs and web automations on monoes.me",
		Long: "The monoes.me library holds workflows, orgs and web automations: official ones " +
			"published by monoes, public ones from the community, and your own private ones.\n\n" +
			"  monoagentcli library login                       # log in (opens the browser)\n" +
			"  monoagentcli library list --kind automation       # official + public automations\n" +
			"  monoagentcli library install automation instagram # download, verify, install\n" +
			"  monoagentcli library publish workflow <id>        # upload (private unless --public)\n" +
			"  monoagentcli library update                       # newer versions of what you installed\n\n" +
			"The host is https://monoes.me unless MONOES_BASE_URL says otherwise. The login is kept per " +
			"profile in its encrypted vault.",
		PersistentPostRun: func(cmd *cobra.Command, args []string) { env.Close() },
	}
	for _, c := range []*cobra.Command{
		newLibraryLoginCmd(env), newLibraryLogoutCmd(env), newLibraryStatusCmd(env),
		newLibraryListCmd(env), newLibraryShowCmd(env), newLibraryInstalledCmd(env),
		newLibraryInstallCmd(env), newLibraryPublishCmd(env), newLibraryUpdateCmd(env),
	} {
		withJSONErrors(cfg, c)
		cmd.AddCommand(c)
	}
	return cmd
}

// libEnv opens the database, the profile's library client and the
// automation registry lazily, once per command.
type libEnv struct {
	cfg    *globalConfig
	db     *storage.Database
	client *library.Client
	reg    *automation.Registry
}

func (e *libEnv) Close() {
	if e.db != nil {
		e.db.Close()
		e.db = nil
	}
}

func (e *libEnv) open() (*library.Client, error) {
	if e.client != nil {
		return e.client, nil
	}
	db, err := initDB(e.cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	e.db = db
	base := library.BaseURL()
	c, err := library.NewClient(base, &library.VaultStore{DB: db.DB, ProfileID: e.cfg.ProfileID, BaseURL: base})
	if err != nil {
		return nil, errInvalidInput("%v", err)
	}
	e.client = c
	return c, nil
}

func (e *libEnv) registry() (*automation.Registry, error) {
	if e.reg != nil {
		return e.reg, nil
	}
	reg, err := openAutomationRegistry()
	if err != nil {
		return nil, err
	}
	e.reg = reg
	return reg, nil
}

func (e *libEnv) provenance() (*library.Provenance, error) {
	reg, err := e.registry()
	if err != nil {
		return nil, err
	}
	return library.OpenProvenance(reg.Home()), nil
}

// requireLogin opens the client and fails with "log in first" (exit 4)
// when the profile has no monoes.me login. Every library read needs one,
// official items included; this check sends nothing.
func (e *libEnv) requireLogin(ctx context.Context) (*library.Client, error) {
	c, err := e.open()
	if err != nil {
		return nil, err
	}
	t, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, libErr(library.ErrNotLoggedIn)
	}
	return c, nil
}

// loginRequiredError is exit 4 with "login_required": true in --json, so
// the app can tell "log in" from a connection failure.
type loginRequiredError struct{ *cliError }

func (e loginRequiredError) Unwrap() error { return e.cliError }

func (loginRequiredError) JSONErrorFields() map[string]any {
	return map[string]any{"login_required": true}
}

// isLoginRequired reports whether err means "log in to monoes.me first".
func isLoginRequired(err error) bool {
	var lr loginRequiredError
	return errors.Is(err, library.ErrNotLoggedIn) || errors.As(err, &lr)
}

// libErr gives a library failure the CLI's exit code: 2 not found, 3
// rejected input, 4 login/connection. No login, or a 401 the token
// refresh did not cure, says "Log in to monoes.me first".
func libErr(err error) error {
	if err == nil {
		return nil
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return err
	}
	var ae *library.APIError
	switch {
	case errors.Is(err, library.ErrNotLoggedIn):
		return loginRequiredError{&cliError{code: 4, msg: library.ErrNotLoggedIn.Error()}}
	case errors.As(err, &ae):
		switch ae.Status {
		case http.StatusNotFound:
			return &cliError{code: 2, msg: err.Error()}
		case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return &cliError{code: 3, msg: err.Error()}
		case http.StatusForbidden, http.StatusTooManyRequests:
			return &cliError{code: 4, msg: err.Error()}
		}
		if ae.Status >= 500 {
			return &cliError{code: 4, msg: err.Error()}
		}
	case errors.Is(err, library.ErrSHA256Mismatch), errors.Is(err, automation.ErrSHA256Mismatch):
		return &cliError{code: 3, msg: err.Error() + " — nothing was installed"}
	case strings.Contains(err.Error(), "monoes.me unreachable"):
		return &cliError{code: 4, msg: err.Error()}
	}
	return err
}

// printLib writes v as indented JSON with --json, else calls human.
func printLib(cfg *globalConfig, cmd *cobra.Command, v any, human func(w io.Writer)) error {
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), v)
	}
	human(cmd.OutOrStdout())
	return nil
}

// openLoginURL opens the authorize URL in the default browser. Windows'
// `start` would split the URL at its '&'s, so it goes through
// url.dll's protocol handler there.
var openLoginURL = func(u string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
	return openURLInBrowser(u)
}

// libStatus is `library status` (and the result of a login).
type libStatus struct {
	BaseURL   string        `json:"base_url"`
	Profile   string        `json:"profile"`
	LoggedIn  bool          `json:"logged_in"`
	User      *library.User `json:"user"`
	Scopes    []string      `json:"scopes"`
	ExpiresAt string        `json:"expires_at,omitempty"`
	Method    string        `json:"method,omitempty"`
	Error     string        `json:"error,omitempty"`
}

func statusFrom(e *libEnv, t *library.Token) libStatus {
	s := libStatus{BaseURL: e.client.BaseURL, Profile: e.cfg.ProfileID, Scopes: []string{}}
	if t == nil {
		return s
	}
	s.LoggedIn, s.User, s.Method = true, t.User, t.Method
	if t.Scope != "" {
		s.Scopes = strings.Fields(t.Scope)
	}
	if !t.ExpiresAt.IsZero() {
		s.ExpiresAt = t.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return s
}

func printStatus(cfg *globalConfig, cmd *cobra.Command, s libStatus) error {
	return printLib(cfg, cmd, s, func(w io.Writer) {
		switch {
		case !s.LoggedIn:
			fmt.Fprintf(w, "Not logged in to %s. Run `monoagentcli library login` to browse and install from the library.\n", s.BaseURL)
		case s.User != nil:
			fmt.Fprintf(w, "Logged in to %s as %s (%s)\n", s.BaseURL, nonEmptyStr(s.User.Username, s.User.Name), s.User.Email)
		default:
			fmt.Fprintf(w, "Logged in to %s\n", s.BaseURL)
		}
		if s.Error != "" {
			fmt.Fprintf(w, "Note: %s\n", s.Error)
		}
	})
}

func nonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func newLibraryStatusCmd(e *libEnv) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether this profile is logged in to monoes.me, and as whom",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.open()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			t, err := c.Token(ctx)
			if err != nil {
				return err
			}
			s := statusFrom(e, t)
			if t == nil || offline {
				return printStatus(e.cfg, cmd, s)
			}
			me, err := c.Me(ctx)
			var ae *library.APIError
			switch {
			case err == nil:
				t2, _ := c.Token(ctx) // refreshed while asking, perhaps
				s = statusFrom(e, t2)
				s.User = &me.User
				if len(me.Scopes) > 0 {
					s.Scopes = me.Scopes
				}
			case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
				s = statusFrom(e, nil)
				s.Error = "the saved login has expired; run `monoagentcli library login`"
			default:
				s.Error = err.Error() // offline: report the saved login
			}
			return printStatus(e.cfg, cmd, s)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "Report the saved login without asking monoes.me")
	return cmd
}

func newLibraryLoginCmd(e *libEnv) *cobra.Command {
	var email, code string
	var send, noBrowser bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to monoes.me (opens the browser; --email for a code by email)",
		Long: "Logs this profile in to monoes.me.\n\n" +
			"By default this opens the browser at monoes.me's sign-in page and waits (up to --timeout) for it " +
			"to redirect back to a one-time listener on 127.0.0.1 (OAuth 2.1 with PKCE). --no-browser only " +
			"prints the URL to open.\n\n" +
			"On a machine without a browser, use a code sent by email:\n" +
			"  monoagentcli library login --email you@example.com            # sends a code, then asks for it\n" +
			"  monoagentcli library login --email you@example.com --send     # only send the code\n" +
			"  monoagentcli library login --email you@example.com --code 123456\n\n" +
			"The login is stored in the profile's encrypted vault and refreshed automatically.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.open()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if email == "" && (send || code != "") {
				return errInvalidInput("--send and --code need --email")
			}
			if email != "" {
				return emailLogin(e, cmd, c, email, code, send)
			}
			stderr := cmd.ErrOrStderr()
			opts := library.LoginOptions{Timeout: timeout, OnURL: func(u string) {
				if e.cfg.JSONOutput {
					b, _ := json.Marshal(map[string]string{"kind": "url", "url": u})
					fmt.Fprintln(stderr, string(b))
					return
				}
				if noBrowser {
					fmt.Fprintf(stderr, "Open this URL to log in to monoes.me:\n\n  %s\n\nWaiting for the sign-in to finish…\n", u)
				} else {
					fmt.Fprintf(stderr, "Opening your browser to log in to monoes.me…\nIf it does not open, visit:\n\n  %s\n\n", u)
				}
			}}
			if !noBrowser {
				opts.Open = openLoginURL
			}
			t, err := c.LoginPKCE(ctx, opts)
			if err != nil {
				if err = libErr(err); exitCodeFor(err) == 1 {
					err = errAuthConnection("%v", err) // no answer, refused, or a bad redirect
				}
				return err
			}
			return printStatus(e.cfg, cmd, statusFrom(e, t))
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Log in with a code sent to this address instead of the browser")
	cmd.Flags().BoolVar(&send, "send", false, "With --email: only send the code (then run again with --code)")
	cmd.Flags().StringVar(&code, "code", "", "With --email: the code from the email")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the sign-in URL instead of opening the browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "How long to wait for the browser sign-in")
	return cmd
}

func emailLogin(e *libEnv, cmd *cobra.Command, c *library.Client, email, code string, send bool) error {
	ctx := cmd.Context()
	if code == "" {
		if err := c.SendEmailCode(ctx, email); err != nil {
			return libErr(err)
		}
		if send || e.cfg.JSONOutput {
			return printLib(e.cfg, cmd, map[string]any{"code_sent": true, "email": email}, func(w io.Writer) {
				fmt.Fprintf(w, "If %s has a monoes.me account, a code is on its way. Then run:\n  monoagentcli library login --email %s --code <code>\n", email, email)
			})
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "If %s has a monoes.me account, a code is on its way.\nCode: ", email)
		line, err := readLine(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("read the code: %w", err)
		}
		code = line
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return errInvalidInput("no code given")
	}
	t, err := c.VerifyEmailCode(ctx, email, code)
	if err != nil {
		var ae *library.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			return errAuthConnection("the code is wrong or expired; ask for a new one with `monoagentcli library login --email %s --send`", email)
		}
		return libErr(err)
	}
	return printStatus(e.cfg, cmd, statusFrom(e, t))
}

func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimSpace(b.String()), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return strings.TrimSpace(b.String()), nil
			}
			return "", err
		}
	}
}

func newLibraryLogoutCmd(e *libEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log this profile out of monoes.me (revokes and forgets the saved login)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.open()
			if err != nil {
				return err
			}
			if err := c.Logout(cmd.Context()); err != nil {
				return err
			}
			return printLib(e.cfg, cmd, map[string]any{"logged_out": true, "base_url": c.BaseURL}, func(w io.Writer) {
				fmt.Fprintf(w, "Logged out of %s.\n", c.BaseURL)
			})
		},
	}
}
