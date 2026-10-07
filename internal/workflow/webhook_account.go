package workflow

import (
	"net/http"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/accountdoor"
)

const (
	// webhookRetryAfter is the Retry-After (seconds) of a request refused for a
	// locked account: callers that retry on a 503 come back after a minute.
	webhookRetryAfter = "60"
	// webhookLockedLogEvery is how often the server logs that it is refusing
	// requests.
	webhookLockedLogEvery = time.Minute
)

// webhookLockedLog spaces the server's "webhook refused" lines: at most one
// per webhookLockedLogEvery. The zero value is ready to use.
type webhookLockedLog struct {
	mu   sync.Mutex
	last time.Time
	now  func() time.Time // nil: time.Now; a test sets it
}

// due reports whether a line is due, and if it is, counts it as written. It
// measures with time.Now, whose monotonic reading a wall clock that is set back
// does not move.
func (l *webhookLockedLog) due() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	at := now()
	if !l.last.IsZero() && at.Sub(l.last) < webhookLockedLogEvery {
		return false
	}
	l.last = at
	return true
}

// refuseWhileLocked answers a request with 503, Retry-After and
// {"error":"login_required"} while the machine holds no valid monoes.me login,
// and reports whether it did. Whatever the method or path, the preflight too: an
// unknown webhook looks like a known one.
func (s *WebhookServer) refuseWhileLocked(w http.ResponseWriter, r *http.Request) bool {
	if account.Require(r.Context()) == nil {
		return false
	}
	if s.lockedLog.due() {
		// Without this line the only sign of a locked daemon at the webhook
		// port is the callers' 503s.
		s.logger.Warn().Msg("webhook refused: no valid monoes.me login (run: monoagentcli account login)")
	}
	w.Header().Set("Retry-After", webhookRetryAfter)
	writeJSONError(w, http.StatusServiceUnavailable, accountdoor.Code)
	return true
}
