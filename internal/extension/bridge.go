package extension

import "github.com/monoes/mono-agent/internal/browser"

// ServerBridge adapts *Server to satisfy browser.ExtensionBridge, breaking the
// import cycle between the browser and extension packages.
type ServerBridge struct {
	Server *Server
}

// Compile-time check.
var _ browser.ExtensionBridge = (*ServerBridge)(nil)

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
