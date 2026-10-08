//go:build !windows

package account_test

import (
	"fmt"
	"syscall"
	"testing"
)

// refusedURL is the URL of a loopback port that refuses connections and that nobody else can take
// for the length of the test: a socket that is bound and never listens. (A server that is started and
// closed gives its port back, and another test or process can bind it before the dial.)
func refusedURL(t testing.TB) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", sa.(*syscall.SockaddrInet4).Port)
}
