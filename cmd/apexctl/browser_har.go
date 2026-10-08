package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const browserHARUsage = `Usage:
  apexctl analysis browser-har list --id RUN [--api URL] [--api-key KEY|--bearer-token TOKEN]
  apexctl analysis browser-har download --id RUN --artifact NAME --out FILE [...]
  apexctl analysis browser-har download --replay SESSION [--evidence] --out FILE [...]
Lists a run archive's HARs (native and synthetic, with fidelity), or downloads
one: a run HAR by name (browser_har_synthetic.har is derived from the run's
retained network entries), or a browser replay's HAR (--evidence: its evidence
ZIP with the report, the HAR and what the HAR is). Downloads need editor
access to the project. --out - writes to standard output; an existing file is
never overwritten.`

// browserHARDownloadTimeout bounds one HAR download (at most 100 MiB).
const browserHARDownloadTimeout = 10 * time.Minute

func executeBrowserHARCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(out, browserHARUsage)
		return err
	}
	action := args[0]
	flags := flag.NewFlagSet("apexctl analysis browser-har "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	id := flags.String("id", "", "managed run ID")
	artifact := flags.String("artifact", "", "HAR name from list")
	replay := flags.String("replay", "", "browser replay session ID")
	evidence := flags.Bool("evidence", false, "download the replay's evidence ZIP")
	output := flags.String("out", "", "file to write, or - for standard output")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, browserHARUsage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("browser-har does not accept positional arguments")
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	switch action {
	case "list":
		if *id == "" || *artifact != "" || *replay != "" || *evidence || *output != "" {
			return errors.New("browser-har list takes --id only")
		}
		if err := analysisCLIScopeAdmission(*id, ""); err != nil {
			return err
		}
		data, err := client.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(*id)+"/archive/browser/har", nil, "")
		if err != nil {
			return err
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err != nil {
			return errors.New("invalid browser HAR list response")
		}
		pretty.WriteByte('\n')
		_, err = out.Write(pretty.Bytes())
		return err
	case "download":
		if *output == "" {
			return errors.New("browser-har download requires --out FILE (or --out -)")
		}
		var path string
		switch {
		case *replay != "" && *id == "" && *artifact == "":
			path = "/api/v1/browser-replay/sessions/" + url.PathEscape(*replay) + "/har"
			if *evidence {
				path = "/api/v1/browser-replay/sessions/" + url.PathEscape(*replay) + "/evidence"
			}
		case *replay == "" && *id != "" && *artifact != "" && !*evidence:
			if err := analysisCLIScopeAdmission(*id, ""); err != nil {
				return err
			}
			if strings.ContainsAny(*artifact, `/\`) || *artifact == "." || *artifact == ".." {
				return errors.New("--artifact must be a HAR name from browser-har list")
			}
			path = "/api/v1/tests/" + url.PathEscape(*id) + "/archive/browser/har/" + url.PathEscape(*artifact)
		default:
			return errors.New("browser-har download takes --id with --artifact, or --replay (optionally --evidence)")
		}
		return client.downloadBrowserHAR(ctx, path, *output, out)
	default:
		return fmt.Errorf("unknown browser-har action %q (list or download)", action)
	}
}

// downloadBrowserHAR streams a HAR download to a new file (or out for -),
// removing a partial file when the download fails.
func (c *ApiClient) downloadBrowserHAR(ctx context.Context, path, output string, out io.Writer) (err error) {
	ctx, cancel := context.WithTimeout(ctx, browserHARDownloadTimeout)
	defer cancel()
	res, err := c.doContext(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return fmt.Errorf("controller returned %s: %s", res.Status, bytes.TrimSpace(data))
	}
	destination := out
	if output != "-" {
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", output, err)
		}
		defer func() {
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(output)
			}
		}()
		destination = file
	}
	written, err := io.Copy(destination, res.Body)
	if err != nil {
		return err
	}
	if output != "-" {
		_, err = fmt.Fprintf(out, "wrote %d bytes to %s (fidelity %s)\n", written, output, firstNonEmptyCLI(res.Header.Get("X-APEXLoad-HAR-Fidelity"), "evidence"))
	}
	return err
}
