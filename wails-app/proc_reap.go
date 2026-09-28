package main

import (
	"os/exec"
	"sync"
)

// chatGroupReaped maps a process-group leader's *exec.Cmd to a channel that
// is closed once Wait has reaped it. Until then its pid, and so the group
// id, can't be reused, so a delayed group SIGKILL is safe; after it, the
// id may belong to someone else, so the kill must be skipped (see
// killChatProcessGroup in proc_unix.go). Tracked from setChatProcessGroup;
// Windows never reads it.
var chatGroupReaped sync.Map // *exec.Cmd -> chan struct{}

func trackChatGroup(cmd *exec.Cmd) {
	chatGroupReaped.Store(cmd, make(chan struct{}))
}

// reapedChan returns the channel closed once cmd is reaped, or nil when
// cmd isn't tracked (already reaped and forgotten, or never tracked).
func reapedChan(cmd *exec.Cmd) chan struct{} {
	if v, ok := chatGroupReaped.Load(cmd); ok {
		return v.(chan struct{})
	}
	return nil
}

// waitChatProcess is cmd.Wait for a command started with
// setChatProcessGroup: it also records that the leader was reaped.
func waitChatProcess(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if v, ok := chatGroupReaped.LoadAndDelete(cmd); ok {
		close(v.(chan struct{}))
	}
	return err
}
