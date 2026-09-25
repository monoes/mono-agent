//go:build social

package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jevpick"
)

func TestJevPickerDisabledByDefault(t *testing.T) {
	var j JevPicker
	var page browser.PageInterface = struct{ browser.PageInterface }{}
	if j.JevAvailable(page) {
		t.Fatal("zero JevPicker must be disabled")
	}
	if _, err := j.JevElement(context.Background(), page, jevpick.Target{Intent: "x", Kind: "click"}); !errors.Is(err, ErrJevUnavailable) {
		t.Fatalf("err = %v, want ErrJevUnavailable", err)
	}
}

func TestSetJevPickerThreshold(t *testing.T) {
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	var j JevPicker
	for _, tc := range []struct{ in, want float64 }{{0, DefaultJevMinP}, {-1, DefaultJevMinP}, {2, DefaultJevMinP}, {0.8, 0.8}} {
		j.SetJevPicker(c, tc.in)
		if j.jevMinP != tc.want {
			t.Fatalf("SetJevPicker(%v): minP = %v, want %v", tc.in, j.jevMinP, tc.want)
		}
	}
	// A page without CDP cannot be picked from.
	if j.JevAvailable(struct{ browser.PageInterface }{}) {
		t.Fatal("page without CDP must not be available")
	}
	j.SetJevPicker(nil, 0)
	if j.jevClient != nil {
		t.Fatal("nil client must disable")
	}
}
