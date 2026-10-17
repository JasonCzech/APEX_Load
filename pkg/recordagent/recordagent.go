package recordagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

const (
	DefaultDynamicAddr    = "127.0.0.1:0"
	DefaultProductionAddr = "127.0.0.1:27777"
	DefaultPairingTTL     = 10 * time.Minute
	DefaultSessionTTL     = 12 * time.Hour
)

type Args struct {
	Addr         string
	Token        string
	TokenStdin   bool
	Format       string
	PairingTTL   time.Duration
	SessionTTL   time.Duration
	AllowOrigins []string
}

func Defaults(addr string) Args {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = DefaultDynamicAddr
	}
	return Args{
		Addr:       addr,
		Format:     "text",
		PairingTTL: DefaultPairingTTL,
		SessionTTL: DefaultSessionTTL,
	}
}

func ParseArgs(argv []string, defaults Args) Args {
	if strings.TrimSpace(defaults.Addr) == "" {
		defaults.Addr = DefaultDynamicAddr
	}
	if strings.TrimSpace(defaults.Format) == "" {
		defaults.Format = "text"
	}
	if defaults.PairingTTL <= 0 {
		defaults.PairingTTL = DefaultPairingTTL
	}
	if defaults.SessionTTL <= 0 {
		defaults.SessionTTL = DefaultSessionTTL
	}

	out := defaults
	for i := 0; i < len(argv); i++ {
		next := func() string {
			if i+1 < len(argv) {
				i++
				return argv[i]
			}
			return ""
		}
		switch argv[i] {
		case "--addr":
			if value := strings.TrimSpace(next()); value != "" {
				out.Addr = value
			}
		case "--token":
			out.Token = strings.TrimSpace(next())
		case "--token-stdin":
			out.TokenStdin = true
		case "--allow-origin":
			if value := strings.TrimSpace(next()); value != "" && value != "*" {
				out.AllowOrigins = append(out.AllowOrigins, strings.TrimRight(value, "/"))
			}
		case "--pairing-ttl":
			if value, err := time.ParseDuration(strings.TrimSpace(next())); err == nil && value > 0 {
				out.PairingTTL = value
			}
		case "--session-ttl":
			if value, err := time.ParseDuration(strings.TrimSpace(next())); err == nil && value > 0 {
				out.SessionTTL = value
			}
		case "--json":
			out.Format = "json"
		}
	}
	return out
}

func Usage(program string, defaultAddr string) string {
	program = strings.TrimSpace(program)
	if program == "" {
		program = "apexload-record-agent"
	}
	defaultAddr = strings.TrimSpace(defaultAddr)
	if defaultAddr == "" {
		defaultAddr = DefaultProductionAddr
	}
	return fmt.Sprintf(`Usage: %s [options]

Starts a foreground localhost bridge so a remote APEX Load dashboard can launch
and control a browser recording session on this workstation, or capture a phone
or tablet: a device capture opens the recording proxy on the network address
chosen in Recording Studio, with its own password, only while it runs.

Options:
  --addr            Listen address (default %s)
  --token           Pairing token (default: generated)
  --token-stdin     Read the pairing token from stdin at startup
  --allow-origin    Pre-authorize a dashboard origin; may be repeated
  --pairing-ttl     Pairing token lifetime (default 10m)
  --session-ttl     Paired WebSocket session lifetime (default 12h)
  --json            Print machine-readable startup details`, program, defaultAddr)
}

func RunCommand(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer, program string, defaults Args) int {
	for _, arg := range argv {
		if arg == "--help" || arg == "-h" || arg == "help" {
			fmt.Fprintln(stderr, Usage(program, defaults.Addr))
			return 0
		}
	}
	args := ParseArgs(argv, defaults)
	if args.TokenStdin && strings.TrimSpace(args.Token) == "" {
		token, err := readTokenFromStdin(stdin, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		args.Token = token
	}
	if err := Run(ctx, args, stdout, program); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func readTokenFromStdin(stdin io.Reader, stderr io.Writer) (string, error) {
	if stdin == nil {
		return "", fmt.Errorf("--token-stdin requires stdin")
	}
	fmt.Fprint(stderr, "Paste the pairing token the Recording Studio shows: ")
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read bridge pairing token from stdin: %w", err)
		}
		return "", fmt.Errorf("read bridge pairing token from stdin: no token provided")
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		return "", fmt.Errorf("read bridge pairing token from stdin: no token provided")
	}
	return token, nil
}

func Run(ctx context.Context, args Args, stdout io.Writer, program string) error {
	token := strings.TrimSpace(args.Token)
	generated := token == ""
	if generated {
		fresh, err := recordbridge.GenerateBrowserRecorderBridgeToken()
		if err != nil {
			return fmt.Errorf("generate bridge token: %w", err)
		}
		token = fresh
	} else if err := recordbridge.ValidateBrowserRecorderBridgePairingToken(token); err != nil {
		return fmt.Errorf("invalid bridge pairing token: %w", err)
	}

	server := recordbridge.NewBrowserRecorderBridgeServer(recordbridge.BrowserRecorderBridgeConfig{
		Addr:           args.Addr,
		PairingToken:   token,
		PairingTTL:     args.PairingTTL,
		SessionTTL:     args.SessionTTL,
		AllowedOrigins: args.AllowOrigins,
		CADir:          agentCADir(),
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	if err := waitForBind(ctx, server, args.Addr, errCh); err != nil {
		return err
	}

	payload := map[string]any{
		"status":             "running",
		"api_origin":         server.BrowserURL(),
		"pairing_expires_at": server.BridgePairingExpiresAt().Format(time.RFC3339),
	}
	// A token the operator supplied is never echoed back; only one the agent
	// generated is printed, since nothing else knows it.
	if generated {
		payload["pairing_token"] = token
	}
	if args.Format == "json" {
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		if strings.TrimSpace(program) == "" {
			program = "APEX Load Recording Agent"
		}
		fmt.Fprintf(stdout, "%s is running.\n", program)
		fmt.Fprintf(stdout, "  Agent address:  %s\n", server.BrowserURL())
		if generated {
			fmt.Fprintf(stdout, "  Pairing token:  %s (generated here)\n", token)
		} else {
			fmt.Fprintln(stdout, "  Pairing token:  the one you supplied")
		}
		fmt.Fprintf(stdout, "  Pair before:    %s\n", server.BridgePairingExpiresAt().Local().Format("15:04:05 MST"))
		fmt.Fprintln(stdout)
		if generated {
			fmt.Fprintln(stdout, "The Recording Studio pairs with the token it shows, not this one: stop this agent")
			fmt.Fprintln(stdout, "and run the command the studio gives you, then click Connect there.")
		} else {
			fmt.Fprintln(stdout, "Return to the Recording Studio and click Connect.")
		}
		fmt.Fprintln(stdout, "Press Ctrl-C to stop the agent.")
	}

	// Replay browsers (verify) close before their Playwright driver exits
	// with the agent.
	defer browserprotocol.ShutdownPool(10 * time.Second)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("bridge stopped: %w", err)
		}
		return nil
	}
}

// agentCADir keeps the device capture's certificate authority in the
// user's configuration directory, so a phone trusts it once whichever folder
// the agent runs from; APEXLOAD_RECORDER_CA_DIR still wins.
func agentCADir() string {
	if strings.TrimSpace(os.Getenv("APEXLOAD_RECORDER_CA_DIR")) != "" {
		return ""
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "APEX Load", "Recording Agent")
}

func InstallBrowsers(stdout io.Writer) error {
	fmt.Fprintln(stdout, "Installing APEX Load recording browser dependencies...")
	if err := recbrowser.EnsureBrowsers(); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Recording browser dependencies are installed.")
	return nil
}

func waitForBind(ctx context.Context, server *recordbridge.Bridge, requestedAddr string, errCh <-chan error) error {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}
			return fmt.Errorf("bridge stopped before binding")
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for bridge listener")
		case <-tick.C:
			bound := server.BoundAddr()
			if bound != "" && bound != requestedAddr && !strings.HasSuffix(bound, ":0") {
				return nil
			}
			if !strings.Contains(requestedAddr, ":0") && bound == requestedAddr {
				return nil
			}
		}
	}
}
