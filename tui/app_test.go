package main

import (
	"testing"
	"time"

	"naivereal/tui/internal/coremgr"
)

func TestWaitLogBlocksUntilLogArrives(t *testing.T) {
	m := model{core: coremgr.NewManager()}
	result := make(chan coreLogMsg, 1)
	go func() {
		result <- m.waitLog()().(coreLogMsg)
	}()
	select {
	case <-result:
		t.Fatal("waitLog returned without a log message")
	case <-time.After(25 * time.Millisecond):
	}
	m.core.Logs <- "ready"
	select {
	case got := <-result:
		if got != "ready" {
			t.Fatalf("log = %q, want ready", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waitLog did not deliver a log message")
	}
}

func TestWaitExitBlocksUntilExitArrives(t *testing.T) {
	m := model{core: coremgr.NewManager()}
	result := make(chan coreExitMsg, 1)
	go func() {
		result <- m.waitExit()().(coreExitMsg)
	}()
	select {
	case <-result:
		t.Fatal("waitExit returned without an exit message")
	case <-time.After(25 * time.Millisecond):
	}
	m.core.Exits <- "stopped"
	select {
	case got := <-result:
		if got != "stopped" {
			t.Fatalf("exit = %q, want stopped", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waitExit did not deliver an exit message")
	}
}
