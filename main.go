// gh-cost-center is a GitHub CLI extension that automates cost center
// assignments for GitHub Copilot users in a GitHub Enterprise.
//
// Install:
//
//	gh extension install renan-alm/gh-cost-center
//
// Usage:
//
//	gh cost-center assign --mode plan
//	gh cost-center assign --mode apply --yes
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/renan-alm/gh-cost-center/cmd"
)

func main() {
	// SIGPIPE can occur when output is piped to head, grep, etc.
	// Exit cleanly instead of crashing with a broken-pipe error.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGPIPE)
	go func() {
		<-sig
		os.Exit(0)
	}()

	// SIGINT (Ctrl-C) — let the Go runtime handle cleanup, but ensure
	// we exit with a non-zero code so callers can detect interruption.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		os.Exit(130) // 128 + SIGINT(2) — standard Unix convention
	}()

	cmd.Execute()
}
