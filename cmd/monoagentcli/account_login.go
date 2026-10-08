package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// signInFlags are the flags of `account login` and of its alias `library login`.
type signInFlags struct {
	email, code     string
	send, noBrowser bool
	codeStdin       bool
	timeout         time.Duration
}

func (f *signInFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.email, "email", "", "Log in with a code sent to this address instead of the browser")
	cmd.Flags().BoolVar(&f.send, "send", false, "With --email: only send the code (then run again with --code)")
	cmd.Flags().StringVar(&f.code, "code", "", "With --email: the code from the email (visible in the process list; prefer --code-stdin)")
	cmd.Flags().BoolVar(&f.codeStdin, "code-stdin", false, "With --email: read the code from stdin, so it never appears in the process list")
	cmd.Flags().BoolVar(&f.noBrowser, "no-browser", false, "Print the sign-in URL instead of opening the browser")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "How long to wait for the browser sign-in")
}

// signIn runs the sign-in the flags ask for and returns the session's status. sent
// is true when it only emailed the code. name is the command, for the hints.
func signIn(cmd *cobra.Command, cfg *globalConfig, f *signInFlags, name string) (st account.Status, sent bool, err error) {
	if f.email == "" && (f.send || f.code != "" || f.codeStdin) {
		return st, false, errInvalidInput("--send, --code and --code-stdin need --email")
	}
	if f.email != "" {
		return emailSignIn(cmd, cfg, f, name)
	}
	stderr := cmd.ErrOrStderr()
	opts := account.LoginOptions{Timeout: f.timeout, OnURL: func(u string) {
		if cfg.JSONOutput {
			b, _ := json.Marshal(map[string]string{"kind": "url", "url": u})
			fmt.Fprintln(stderr, string(b))
			return
		}
		if f.noBrowser {
			fmt.Fprintf(stderr, "Open this URL to log in to monoes.me:\n\n  %s\n\nWaiting for the sign-in to finish…\n", u)
		} else {
			fmt.Fprintf(stderr, "Opening your browser to log in to monoes.me…\nIf it does not open, visit:\n\n  %s\n\n", u)
		}
	}}
	if !f.noBrowser {
		opts.Open = openLoginURL
	}
	st, err = account.Login(cmd.Context(), opts)
	return st, false, signInErr(err)
}

func emailSignIn(cmd *cobra.Command, cfg *globalConfig, f *signInFlags, name string) (st account.Status, sent bool, err error) {
	ctx := cmd.Context()
	code := f.code
	if f.codeStdin {
		if f.code != "" || f.send {
			return st, false, errInvalidInput("--code-stdin cannot be combined with --code or --send")
		}
		line, err := readLine(cmd.InOrStdin())
		if err != nil {
			return st, false, fmt.Errorf("read the code from stdin: %w", err)
		}
		if code = line; code == "" {
			return st, false, errInvalidInput("no code given on stdin")
		}
	}
	if code == "" {
		if err := account.SendEmailCode(ctx, f.email); err != nil {
			return st, false, signInErr(err)
		}
		if f.send || cfg.JSONOutput {
			err := printLib(cfg, cmd, map[string]any{"code_sent": true, "email": f.email}, func(w io.Writer) {
				fmt.Fprintf(w, "If %s has a monoes.me account, a code is on its way. Then run:\n  monoagentcli %s --email %s --code <code>\n", f.email, name, f.email)
			})
			return st, true, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "If %s has a monoes.me account, a code is on its way.\nCode: ", f.email)
		line, err := readLine(cmd.InOrStdin())
		if err != nil {
			return st, false, fmt.Errorf("read the code: %w", err)
		}
		code = line
	}
	if code = strings.TrimSpace(code); code == "" {
		return st, false, errInvalidInput("no code given")
	}
	st, err = account.VerifyEmailCode(ctx, f.email, code)
	if errors.Is(err, account.ErrBadCode) {
		return st, false, errAuthConnection("the code is wrong or expired; ask for a new one with `monoagentcli %s --email %s --send`", name, f.email)
	}
	return st, false, signInErr(err)
}

// signInErr gives a sign-in failure the CLI's exit code: 4 for anything between
// the user and monoes.me (no answer, a refusal, a wrong code), and the error as it
// is for a local failure such as a missing key store.
func signInErr(err error) error {
	var ce *cliError
	if err == nil || errors.As(err, &ce) || errors.Is(err, account.ErrKeyringUnavailable) {
		return err
	}
	return errAuthConnection("%v", err)
}
