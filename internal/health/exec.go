package health

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// RunStreaming runs a command, passing each stdout/stderr line to progress,
// and returns an error carrying the last lines when it fails.
func RunStreaming(ctx context.Context, progress func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	var mu sync.Mutex
	var tail []string
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			progress(line)
			mu.Lock()
			tail = append(tail, line)
			if len(tail) > 5 {
				tail = tail[1:]
			}
			mu.Unlock()
		}
		io.Copy(io.Discard, pr)
	}()

	err := cmd.Run()
	pw.Close()
	<-scanDone
	if err != nil {
		mu.Lock()
		defer mu.Unlock()
		msg := fmt.Sprintf("%s %s: %v", name, strings.Join(args, " "), err)
		if len(tail) > 0 {
			msg += "\n" + strings.Join(tail, "\n")
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
