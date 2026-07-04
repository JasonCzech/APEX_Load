package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/recordagent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			info := buildinfo.Get()
			fmt.Printf("apexload-record-agent %s (%s, %s)\n", info.DisplayVersion, info.Commit, info.BuildTime)
			return
		case "install-browsers":
			if err := recordagent.InstallBrowsers(os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "error: install recording browsers: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := recordagent.RunCommand(
		ctx,
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		"apexload-record-agent",
		recordagent.Defaults(recordagent.DefaultProductionAddr),
	)
	if code != 0 {
		os.Exit(code)
	}
}
