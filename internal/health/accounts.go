package health

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// GroupAccounts covers the credentials the active profile has saved.
const GroupAccounts = "accounts"

const (
	CheckConnections = "accounts.connections"
	CheckAIProviders = "accounts.ai_connections"
	CheckLogins      = "accounts.logins"

	// FixConnectionRefresh and FixConnectionTryRefresh are parameterized by
	// connection id. The first is auto, offered when the service refused
	// the token (401/403) or it is past its expiry; the second asks first,
	// for a failure that may have another cause: an exchange can rotate
	// the stored refresh token.
	FixConnectionRefresh    = "accounts.connection.refresh"
	FixConnectionTryRefresh = "accounts.connection.try_refresh"
	FixReconnect            = "accounts.connection.reconnect"
	FixAIProviderKey        = "accounts.ai_connection.edit"
	FixLogin                = "accounts.login"
)

const accountTimeout = 2 * time.Minute

// Variables so tests can shrink them.
var (
	// Connections and AI connections are tested accountParallel at a time,
	// each for at most accountItemTimeout, so a few slow services cannot
	// run the whole check into its timeout and lose every row.
	accountParallel    = 4
	accountItemTimeout = 20 * time.Second
	// accountMargin is kept free before the check's own deadline to
	// report the rows.
	accountMargin = 2 * time.Second
)

func accountChecks() []Check {
	return []Check{
		{ID: CheckConnections, Group: GroupAccounts, Title: "Connections", Features: []string{"workflow nodes using them"},
			DependsOn: []string{CheckProfile}, Network: true, Timeout: accountTimeout, Run: checkConnections},
		{ID: CheckAIProviders, Group: GroupAccounts, Title: "AI connections (legacy)", Features: []string{"AI nodes using them"},
			DependsOn: []string{CheckProfile}, Network: true, Timeout: accountTimeout, Run: checkAIProviders},
		{ID: CheckLogins, Group: GroupAccounts, Title: "Platform logins", Features: []string{"social platform actions"},
			DependsOn: []string{CheckProfile}, Run: checkLogins},
	}
}

func accountFixes() []Fix {
	manual := func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }
	refresh := func(ctx context.Context, env *Env, id string, progress func(string)) error {
		if env.RefreshConnection == nil {
			return fmt.Errorf("not available here")
		}
		progress("exchanging the stored refresh token for connection " + id)
		return env.RefreshConnection(ctx, id)
	}
	return []Fix{
		{FixInfo: FixInfo{ID: FixConnectionRefresh, Label: "Refresh the connection's token", Safety: SafetyAuto,
			Command: "monoagentcli connect refresh {arg}"}, ApplyArg: refresh},
		{FixInfo: FixInfo{ID: FixConnectionTryRefresh, Label: "Try refreshing the connection's token", Safety: SafetyConfirm,
			Command: "monoagentcli connect refresh {arg}"}, ApplyArg: refresh},
		// Rows replace these generic commands with their own (FixCommand).
		{FixInfo: FixInfo{ID: FixReconnect, Label: "Reconnect the account", Safety: SafetyManual,
			Command: "monoagentcli connect <platform> (or Connections in the app)"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixAIProviderKey, Label: "Update the API key", Safety: SafetyManual,
			Command: "Settings › AI connections (legacy), or: monoagentcli ai provider add"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixLogin, Label: "Log in again", Safety: SafetyManual,
			Command: "monoagentcli login <platform>"}, Apply: manual},
	}
}

// Test failures of accounts, as the hooks classify them.
type failureKind int

const (
	failUnknown     failureKind = iota
	failRejected                // the service refused the credentials, or the token expired
	failUnreachable             // no proper answer: timeout, network, 5xx, rate limit
)

type accountError struct {
	kind failureKind
	err  error
}

func (e *accountError) Error() string { return e.err.Error() }
func (e *accountError) Unwrap() error { return e.err }

// CredentialsRejected marks a test error as the service refusing the
// credentials (HTTP 401/403) or the token being past its expiry: the only
// failures a silent token refresh is offered for without asking.
func CredentialsRejected(err error) error { return &accountError{failRejected, err} }

// Unreachable marks a test error as the service not answering properly
// (timeout, network, 5xx, rate limit): nothing is known about the
// credentials, so no fix is offered.
func Unreachable(err error) error { return &accountError{failUnreachable, err} }

func classify(err error) failureKind {
	var ae *accountError
	if errors.As(err, &ae) {
		return ae.kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failUnreachable
	}
	return failUnknown
}

// errNotTested marks a row the check ran out of time for.
var errNotTested = errors.New("not tested: the check ran out of time")

// testEach runs test for 0..n-1, accountParallel at a time, each bounded
// by accountItemTimeout — also when test ignores its context. Rows not
// started before the check's deadline (less accountMargin) get
// errNotTested, so the check still returns in time with every row.
func testEach(ctx context.Context, n int, test func(ctx context.Context, i int) error) []error {
	errs := make([]error, n)
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, dl.Add(-accountMargin))
		defer cancel()
	}
	sem := make(chan struct{}, accountParallel)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
			if ctx.Err() != nil {
				<-sem
				errs[i] = errNotTested
				continue
			}
		case <-ctx.Done():
			errs[i] = errNotTested
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = testOne(ctx, i, test)
		}(i)
	}
	wg.Wait()
	return errs
}

func testOne(ctx context.Context, i int, test func(ctx context.Context, i int) error) error {
	ictx, cancel := context.WithTimeout(ctx, accountItemTimeout)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- fmt.Errorf("test crashed: %v", p)
			}
		}()
		ch <- test(ictx, i)
	}()
	select {
	case err := <-ch:
		return err
	case <-ictx.Done():
		if ctx.Err() != nil {
			return Unreachable(errors.New("cut off: the check ran out of time"))
		}
		return Unreachable(fmt.Errorf("no answer within %s", accountItemTimeout))
	}
}

// accountRow sets a tested row's status from its test error: ok, skip
// (not tested), warn (unreachable, nothing to fix) or fail. It reports
// whether the row failed, for the caller to attach a fix.
func accountRow(child *Result, err error, tally *accountTally) bool {
	switch {
	case err == nil:
		child.Status = StatusOK
		return false
	case errors.Is(err, errNotTested):
		tally.untested++
		child.Status, child.Detail = StatusSkip, err.Error()
		return false
	case classify(err) == failUnreachable:
		tally.unreachable++
		child.Status = StatusWarn
		child.Detail = err.Error() + "\nthe service did not answer properly; run the check again later"
		return false
	}
	tally.failing++
	child.Status, child.Detail = StatusFail, err.Error()
	return true
}

type accountTally struct{ total, failing, unreachable, untested int }

// finish sets res's status and summary: "all N <okWord>" or what isn't.
func (t accountTally) finish(res *Result, okWord string) {
	var parts []string
	if t.failing > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d failing", t.failing, t.total))
	}
	if t.unreachable > 0 {
		parts = append(parts, fmt.Sprintf("%d not answering", t.unreachable))
	}
	if t.untested > 0 {
		parts = append(parts, fmt.Sprintf("%d not tested in time", t.untested))
	}
	if len(parts) == 0 {
		res.Status, res.Summary = StatusOK, fmt.Sprintf("all %d %s", t.total, okWord)
		return
	}
	res.Status, res.Summary = StatusWarn, strings.Join(parts, ", ")
}

func checkConnections(ctx context.Context, env *Env) Result {
	if env.Connections == nil || env.TestConnection == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	conns, err := env.Connections(ctx)
	if err != nil {
		return Result{Status: StatusFail, Summary: "cannot list connections", Detail: err.Error()}
	}
	if len(conns) == 0 {
		return Result{Status: StatusInfo, Summary: "none saved"}
	}
	errs := testEach(ctx, len(conns), func(ctx context.Context, i int) error { return env.TestConnection(ctx, conns[i].ID) })
	res, tally := Result{}, accountTally{total: len(conns)}
	for i, c := range conns {
		platform := orDefault(c.Platform, "(no platform)")
		child := Result{ID: "accounts.connection." + shortID(c.ID), Title: platform,
			Summary: orDefault(c.Label, "connection "+shortID(c.ID))}
		if accountRow(&child, errs[i], &tally) {
			refreshable := c.Method == "oauth" && c.HasRefreshToken
			switch {
			case refreshable && classify(errs[i]) == failRejected:
				child.FixID = FixConnectionRefresh + ":" + c.ID
			case refreshable:
				child.FixID = FixConnectionTryRefresh + ":" + c.ID
			default:
				child.FixID = FixReconnect
				child.FixCommand = "monoagentcli connect " + orDefault(c.Platform, "<platform>") + " (or Connections in the app)"
			}
		}
		res.Children = append(res.Children, child)
	}
	tally.finish(&res, "working")
	return res
}

func checkAIProviders(ctx context.Context, env *Env) Result {
	if env.AIProviders == nil || env.TestAIProvider == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	providers, err := env.AIProviders(ctx)
	if err != nil {
		return Result{Status: StatusFail, Summary: "cannot list AI connections", Detail: err.Error()}
	}
	if len(providers) == 0 {
		return Result{Status: StatusInfo, Summary: "none configured"}
	}
	errs := testEach(ctx, len(providers), func(ctx context.Context, i int) error { return env.TestAIProvider(ctx, providers[i].ID) })
	res, tally := Result{}, accountTally{total: len(providers)}
	for i, p := range providers {
		child := Result{ID: "accounts.ai_connection." + shortID(p.ID), Title: p.Name,
			Summary: joinNonEmpty(" · ", p.ProviderID, p.Model)}
		if accountRow(&child, errs[i], &tally) {
			child.FixID = FixAIProviderKey
			child.FixCommand = aiKeyCommand(p)
		}
		res.Children = append(res.Children, child)
	}
	tally.finish(&res, "answering")
	return res
}

// aiKeyCommand is how to give one AI connection a new key from the CLI,
// which has no edit command: add it again, then delete the old one.
func aiKeyCommand(p ProviderInfo) string {
	add := fmt.Sprintf("monoagentcli ai provider add --name %q --provider %s", p.Name, orDefault(p.ProviderID, "<provider>"))
	if p.Model != "" {
		add += " --model " + p.Model
	}
	return fmt.Sprintf("Settings › AI connections (legacy), or: %s --api-key <new key> && monoagentcli ai provider delete %s", add, p.ID)
}

func checkLogins(ctx context.Context, env *Env) Result {
	if env.LoginSessions == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	sessions, err := env.LoginSessions(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "cannot read login sessions", Detail: err.Error()}
	}
	if len(sessions) == 0 {
		return Result{Status: StatusInfo, Summary: "no platform logins saved"}
	}
	ids := loginRowIDs(sessions)
	res, expired := Result{}, 0
	now := time.Now()
	for i, s := range sessions {
		child := Result{ID: ids[i], Title: orDefault(s.Platform, "(no platform)")}
		if s.Expiry.Before(now) {
			expired++
			child.Status, child.FixID = StatusWarn, FixLogin
			child.FixCommand = "monoagentcli login " + orDefault(s.Platform, "<platform>")
			child.Summary = fmt.Sprintf("%s — expired %s", s.Username, s.Expiry.Format("2006-01-02"))
		} else {
			child.Status = StatusOK
			child.Summary = fmt.Sprintf("%s — valid until %s", s.Username, s.Expiry.Format("2006-01-02"))
		}
		res.Children = append(res.Children, child)
	}
	if expired > 0 {
		res.Status, res.Summary = StatusWarn, fmt.Sprintf("%d of %d expired", expired, len(sessions))
	} else {
		res.Status, res.Summary = StatusOK, fmt.Sprintf("%d valid", len(sessions))
	}
	return res
}

// loginRowIDs gives each session a row id that stays the same across runs:
// "accounts.login.<platform>.<username>". A profile holds one session per
// platform and username (the table's unique key), so the pair names it
// even when logging in again replaces the row. Characters other than
// letters, digits, '-' and '_' become '_'; names that collide after that
// get a hash of the real username appended.
func loginRowIDs(sessions []SessionInfo) []string {
	ids := make([]string, len(sessions))
	count := map[string]int{}
	for i, s := range sessions {
		ids[i] = "accounts.login." + idPart(orDefault(s.Platform, "none")) + "." + idPart(orDefault(s.Username, "none"))
		count[ids[i]]++
	}
	for i, s := range sessions {
		if count[ids[i]] > 1 {
			sum := sha1.Sum([]byte(s.Platform + "\x00" + s.Username))
			ids[i] += "-" + hex.EncodeToString(sum[:4])
		}
	}
	return ids
}

func idPart(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
