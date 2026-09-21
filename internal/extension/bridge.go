package extension

import (
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/capture"
)

// ServerBridge adapts *Server to satisfy browser.ExtensionBridge, breaking the
// import cycle between the browser and extension packages.
type ServerBridge struct {
	Server *Server
}

// Compile-time checks. Capturer is deliberately not part of
// browser.ExtensionBridge: capturing is specific to the extension (only a
// real, logged-in Chrome can produce an MHTML archive of a page the user
// is signed into), so callers type-assert for it instead.
var (
	_ browser.ExtensionBridge = (*ServerBridge)(nil)
	_ Capturer                = (*ServerBridge)(nil)
)

func (b *ServerBridge) IsConnected() bool {
	return b.Server.IsConnected()
}

func (b *ServerBridge) CreateTab(url string) (int, error) {
	return b.Server.CreateTab(url)
}

func (b *ServerBridge) NewPage(tabID int) browser.PageInterface {
	return NewExtensionPage(b.Server, tabID)
}

func (b *ServerBridge) CloseTab(tabID int) error {
	return b.Server.CloseTab(tabID)
}

// CapturePage captures a tab through this process's own extension
// connection and writes the envelope to the inbox.
func (b *ServerBridge) CapturePage(req CaptureRequest) (*capture.Result, error) {
	return b.Server.CapturePage(req)
}

// Addr returns the address this server bound, or ("", false) before it has
// bound one. The port is not always the default: listenCandidates falls back
// to FallbackExtensionPort when another process already holds 9222, and
// ensureExtensionConnected uses this to say where the bridge actually is
// when the extension fails to turn up.
func (b *ServerBridge) Addr() (string, bool) {
	return b.Server.Addr()
}

// PairingURL returns the one-time auto-pairing URL for this server, or
// ("", false) if the server hasn't finished binding yet (the caller is
// expected to retry — see ensureExtensionConnected's poll loop in
// cmd/monoagentcli/chrome_helper.go). Only ServerBridge implements this
// (not RemoteBridge): when this process is relaying through another one
// that already owns the extension server, that other process is the one
// that offered pairing, and a second auto-opened tab would just be noise.
func (b *ServerBridge) PairingURL() (string, bool) {
	addr, ready := b.Server.Addr()
	if !ready {
		return "", false
	}
	nonce, err := b.Server.CreatePairingNonce()
	if err != nil {
		return "", false
	}
	return "http://" + addr + "/monoagent/pair?n=" + nonce, true
}
