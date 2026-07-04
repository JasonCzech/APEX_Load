package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonczech/apexload/pkg/recordagent"
)

func recordAgentUsage() {
	fmt.Fprintln(os.Stderr, recordagent.Usage("apexctl record-agent", recordagent.DefaultDynamicAddr))
}

func recordAgentCmd() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := recordagent.RunCommand(
		ctx,
		os.Args[2:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		"apexctl record-agent",
		recordagent.Defaults(recordagent.DefaultDynamicAddr),
	)
	if code != 0 {
		os.Exit(code)
	}
}
