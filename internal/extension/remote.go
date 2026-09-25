package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/capture"
)

// RemoteSender dispatches Commands through another local process's Server
// (typically the daemon) over its HTTP relay, instead of holding a WebSocket
// connection to the extension itself. This lets a short-lived CLI invocation
// share an already-connected extension session rather than racing another
// process for the fixed extension port.
type RemoteSender struct {
	baseURL string
	token   string
	// client has no Timeout of its own: every relayed command has its own
	// deadline (the command's timeout plus relaySlack, see SendCommand). A
	// fixed client cap silently cut every command longer than it — a
	// three-minute pick_element died at 90s, reported as a dead bridge.
	client *http.Client
	// slack overrides relaySlack (tests).
	slack time.Duration
}

// relaySlack is how much longer than the command's own timeout the relay
// request may take: the relaying process waits the full timeout for the
// extension and still has to write the reply back.
const relaySlack = 10 * time.Second

func (r *RemoteSender) requestSlack() time.Duration {
	if r.slack > 0 {
		return r.slack
	}
	return relaySlack
}

// NewRemoteSender creates a sender that relays through the server at baseURL
// (e.g. "http://127.0.0.1:9222"). Callers only construct this after Probe
// confirms a server is already listening there, so its token file is
// already written; if the token can't be read, requests are sent without
// one and the server's handleRelay rejects them with a clear 401 rather
// than proceeding unauthenticated.
func NewRemoteSender(baseURL string) *RemoteSender {
	token, _ := loadToken()
	return &RemoteSender{baseURL: baseURL, token: token, client: &http.Client{}}
}

// Probe reports whether a Server is actually listening and reachable at
// baseURL. Used to decide whether to relay through an existing process or
// fall back to starting a local server. It goes through FetchStatus so
// "something answered" is never mistaken for "a bridge answered": relaying
// commands into a Chrome CDP (or anything else that happens to hold the
// port) would wait forever for a reply that cannot come.
func Probe(baseURL string) bool {
	_, err := FetchStatus(baseURL)
	return err == nil
}

func (r *RemoteSender) SendCommand(cmd *Command, timeout time.Duration) (*Response, error) {
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	if timeout <= 0 {
		timeout = defaultTimeout // what handleRelay applies to a missing timeout
	}
	url := fmt.Sprintf("%s/monoagent/relay?timeout_ms=%d", r.baseURL, timeout.Milliseconds())
	ctx, cancel := context.WithTimeout(context.Background(), timeout+r.requestSlack())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tokenHeader, r.token)
	httpResp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// Same wording as Server.SendCommand's own timeout, so callers
			// classify both the same way.
			return nil, fmt.Errorf("command %s timed out after %s (relayed)", cmd.Type, timeout)
		}
		return nil, fmt.Errorf("relay request: %w", err)
	}
	defer httpResp.Body.Close()

	var resp Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode relay response: %w", err)
	}
	if !resp.Success {
		return &resp, fmt.Errorf("extension error: %s", resp.Error)
	}
	return &resp, nil
}

// IsConnected reports whether the remote server currently has a live
// extension connection.
func (r *RemoteSender) IsConnected() bool {
	st, err := FetchStatus(r.baseURL)
	return err == nil && st.Connected
}

// Status returns the remote bridge's full self-description — what it bound,
// how long it has been up, and whether it is connected, waiting or turning
// the extension away unpaired. See status.go.
func (r *RemoteSender) Status() (Status, error) {
	return FetchStatus(r.baseURL)
}

// CreateTab asks the remote server's extension to open a new tab.
func (r *RemoteSender) CreateTab(url string) (int, error) {
	resp, err := r.SendCommand(&Command{
		Type:   CmdCreateTab,
		Params: map[string]interface{}{"url": url},
	}, 30*time.Second)
	if err != nil {
		return 0, err
	}
	dataMap, _ := resp.Data.(map[string]interface{})
	if dataMap == nil {
		return 0, fmt.Errorf("create_tab response missing data")
	}
	tabIDRaw, ok := dataMap["tabId"]
	if !ok {
		return 0, fmt.Errorf("create_tab response missing tabId")
	}
	tabID, ok := tabIDRaw.(float64)
	if !ok {
		return 0, fmt.Errorf("tabId is not a number: %T", tabIDRaw)
	}
	return int(tabID), nil
}

// CloseTab asks the remote server's extension to close a tab.
func (r *RemoteSender) CloseTab(tabID int) error {
	_, err := r.SendCommand(&Command{
		Type:  CmdCloseTab,
		TabID: tabID,
	}, 30*time.Second)
	return err
}

// RemoteBridge adapts *RemoteSender to satisfy browser.ExtensionBridge, the
// same way ServerBridge adapts a locally-owned *Server.
type RemoteBridge struct {
	Sender *RemoteSender
}

var (
	_ browser.ExtensionBridge = (*RemoteBridge)(nil)
	_ Capturer                = (*RemoteBridge)(nil)
)

func (b *RemoteBridge) IsConnected() bool {
	return b.Sender.IsConnected()
}

func (b *RemoteBridge) CreateTab(url string) (int, error) {
	return b.Sender.CreateTab(url)
}

func (b *RemoteBridge) NewPage(tabID int) browser.PageInterface {
	return NewExtensionPage(b.Sender, tabID)
}

func (b *RemoteBridge) CloseTab(tabID int) error {
	return b.Sender.CloseTab(tabID)
}

// CapturePage relays a capture through the process that owns the extension
// connection; that process is the one that writes the envelope.
func (b *RemoteBridge) CapturePage(req CaptureRequest) (*capture.Result, error) {
	return b.Sender.CapturePage(req)
}
