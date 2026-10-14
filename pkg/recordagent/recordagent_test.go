package recordagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	args := ParseArgs([]string{
		"--addr", "127.0.0.1:7777",
		"--token", "fixed-token",
		"--token-stdin",
		"--allow-origin", "https://controller.example.test/",
		"--allow-origin", "*",
		"--pairing-ttl", "2m",
		"--session-ttl", "1h",
		"--json",
	}, Defaults(DefaultProductionAddr))

	if args.Addr != "127.0.0.1:7777" {
		t.Fatalf("Addr = %q", args.Addr)
	}
	if args.Token != "fixed-token" {
		t.Fatalf("Token = %q", args.Token)
	}
	if !args.TokenStdin {
		t.Fatalf("TokenStdin = false, want true")
	}
	if len(args.AllowOrigins) != 1 || args.AllowOrigins[0] != "https://controller.example.test" {
		t.Fatalf("AllowOrigins = %#v", args.AllowOrigins)
	}
	if args.PairingTTL != 2*time.Minute {
		t.Fatalf("PairingTTL = %s", args.PairingTTL)
	}
	if args.SessionTTL != time.Hour {
		t.Fatalf("SessionTTL = %s", args.SessionTTL)
	}
	if args.Format != "json" {
		t.Fatalf("Format = %q", args.Format)
	}
}

func TestDefaultsDistinguishProductionAndDynamicAddress(t *testing.T) {
	production := Defaults(DefaultProductionAddr)
	if production.Addr != "127.0.0.1:27777" {
		t.Fatalf("production Addr = %q", production.Addr)
	}

	dynamic := Defaults(DefaultDynamicAddr)
	if dynamic.Addr != "127.0.0.1:0" {
		t.Fatalf("dynamic Addr = %q", dynamic.Addr)
	}
}

func TestRunCommandHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunCommand(t.Context(), []string{"--help"}, nil, &stdout, &stderr, "apexload-record-agent", Defaults(DefaultProductionAddr))
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	help := stderr.String()
	for _, want := range []string{
		"Usage: apexload-record-agent [options]",
		"default 127.0.0.1:27777",
		"--allow-origin",
		"--token-stdin",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
}

func TestRunCommandReadsPairingTokenFromStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunCommand(
		t.Context(),
		[]string{"--token-stdin"},
		strings.NewReader("short\n"),
		&stdout,
		&stderr,
		"apexload-record-agent",
		Defaults(DefaultDynamicAddr),
	)
	if code != 1 {
		t.Fatalf("code = %d, want validation failure", code)
	}
	if !strings.Contains(stderr.String(), "Paste the pairing token the Recording Studio shows: ") {
		t.Fatalf("stderr missing prompt: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid bridge pairing token") {
		t.Fatalf("stderr missing validation error: %q", stderr.String())
	}
}

func TestRunRejectsWeakPairingToken(t *testing.T) {
	var stdout bytes.Buffer
	err := Run(t.Context(), Args{
		Addr:  DefaultDynamicAddr,
		Token: "short",
	}, &stdout, "apexload-record-agent")
	if err == nil {
		t.Fatal("Run returned nil error for weak pairing token")
	}
	if !strings.Contains(err.Error(), "invalid bridge pairing token") {
		t.Fatalf("error = %v, want invalid token detail", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// stopAfterStartup stops the agent once it starts printing; Run prints all of
// its startup text before it waits on the context, so nothing is cut short and
// no deadline races server start-up.
type stopAfterStartup struct {
	bytes.Buffer
	stop context.CancelFunc
}

func (w *stopAfterStartup) Write(p []byte) (int, error) {
	w.stop()
	return w.Buffer.Write(p)
}

func runUntilStarted(t *testing.T, args Args) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out := &stopAfterStartup{stop: cancel}
	if err := Run(ctx, args, out, "apexload-record-agent"); err != nil {
		t.Fatalf("Run(format %q) = %v", args.Format, err)
	}
	return out.String()
}

// The startup text names the studio's real control, and a token the operator
// supplied is never echoed back to the terminal or JSON output.
func TestRunNeverEchoesASuppliedPairingToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, format := range []string{"", "json"} {
		out := runUntilStarted(t, Args{Addr: DefaultDynamicAddr, Token: token, Format: format})
		if strings.Contains(out, token) {
			t.Fatalf("output (%q) echoed the supplied token:\n%s", format, out)
		}
		if format == "" && (!strings.Contains(out, "Return to the Recording Studio and click Connect.") || strings.Contains(out, "Pair Local Bridge")) {
			t.Fatalf("startup text = %s", out)
		}
	}

	out := runUntilStarted(t, Args{Addr: DefaultDynamicAddr})
	if !strings.Contains(out, "(generated here)") || !strings.Contains(out, "run the command the studio gives you") {
		t.Fatalf("a generated token is printed with how to pair instead: %s", out)
	}
}
