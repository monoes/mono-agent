//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSetChatProcessGroupCancelOnlyForCommandContext(t *testing.T) {
	plain := exec.Command("x")
	setChatProcessGroup(plain)
	if plain.Cancel != nil {
		t.Fatal("a plain exec.Command must keep a nil Cancel, or Start refuses it")
	}
	withCtx := exec.CommandContext(context.Background(), "x")
	setChatProcessGroup(withCtx)
	if withCtx.Cancel == nil {
		t.Fatal("CommandContext lost its Cancel")
	}
	if withCtx.SysProcAttr == nil || !withCtx.SysProcAttr.HideWindow {
		t.Fatal("chat subprocess must not open a console window")
	}
}

// The chat stop path cancels the ctx first: the tree kill must still end
// the child and let Wait return.
func TestChatCancelKillsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChatHelperSleep$")
	cmd.Env = append(os.Environ(), "MONOAGENT_CHAT_HELPER=1")
	setChatProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not end the chat subprocess")
	}
	killChatProcessGroup(cmd) // after Wait: must be a harmless no-op
}

func TestChatHelperSleep(t *testing.T) {
	if os.Getenv("MONOAGENT_CHAT_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
