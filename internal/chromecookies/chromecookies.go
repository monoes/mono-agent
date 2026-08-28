// Package chromecookies holds the on-disk shape of a Chrome session cookie
// as stored in crawler_sessions.cookies_json. The login flow converts raw
// chrome.cookies.getAll() results from the extension bridge into this shape.
package chromecookies

// Cookie mirrors proto.NetworkCookieParam's JSON shape (name, value, domain,
// path, secure, httpOnly, expires), which is what crawler_sessions.cookies_json
// already stores and what run.go's session-restore unmarshals into — so no
// downstream code needs to change to consume cookies read this way.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
}
