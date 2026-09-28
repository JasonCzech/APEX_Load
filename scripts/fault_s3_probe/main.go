// fault_s3_probe checks the disposable harness S3 service through a loopback
// port-forward. It never discovers credentials, endpoints or buckets implicitly.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const serviceHost = "apexload-s3.apexload-fault.svc"
const bucket = "apexload-archives"

type inputs struct {
	run, access, secret string
	ca                  []byte
}

type evidence struct {
	RunID      string    `json:"run_id"`
	Mode       string    `json:"mode"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Passed     bool      `json:"passed"`
	Stage      string    `json:"stage"`
}

func readBounded(root, name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || len(b) > 32768 {
		return nil, errors.New("private input unavailable")
	}
	return b, nil
}

func loadInputs(root string) (inputs, error) {
	var in inputs
	if !filepath.IsAbs(root) {
		return in, errors.New("absolute inputs required")
	}
	// Duplicate fields must not change which installation or credential is used.
	readObject := func(name string) (map[string]string, error) {
		b, err := readBounded(root, name)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		token, err := dec.Token()
		if err != nil || token != json.Delim('{') {
			return nil, errors.New("invalid inputs")
		}
		obj := map[string]string{}
		for dec.More() {
			token, err = dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, exists := obj[key]; exists {
				return nil, errors.New("duplicate key")
			}
			var value string
			if err = dec.Decode(&value); err != nil {
				return nil, err
			}
			obj[key] = value
		}
		if _, err = dec.Token(); err != nil {
			return nil, err
		}
		if _, err = dec.Token(); err != io.EOF {
			return nil, errors.New("trailing input")
		}
		return obj, nil
	}
	own, err := readObject("ownership.json")
	if err != nil {
		return in, err
	}
	in.run = own["run_id"]
	if !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(in.run) || own["cluster"] != "apexload-fault-"+in.run || own["namespace"] != "apexload-fault" || own["namespace_label"] != "apexload.io/fault-run="+in.run {
		return in, errors.New("invalid ownership")
	}
	creds, err := readObject("credentials.json")
	if err != nil {
		return in, err
	}
	in.access, in.secret = creds["s3_access_key"], creds["s3_secret_key"]
	valid := regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
	if !valid.MatchString(in.access) || !valid.MatchString(in.secret) || in.access == in.secret {
		return in, errors.New("invalid credentials")
	}
	in.ca, err = readBounded(root, "ca.crt")
	return in, err
}

func clientFor(in inputs, address string) (*s3.Client, *http.Transport, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || host != "127.0.0.1" {
		return nil, nil, errors.New("loopback IPv4 port-forward required")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(in.ca) {
		return nil, nil, errors.New("invalid CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 16384,
		DialContext: func(ctx context.Context, network, destination string) (net.Conn, error) {
			h, p, err := net.SplitHostPort(destination)
			if err != nil || p != "8333" || (h != serviceHost && h != bucket+"."+serviceHost && h != "apexload-unowned."+serviceHost) {
				return nil, errors.New("unexpected destination")
			}
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
		}}
	httpClient := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(in.access, in.secret, ""), HTTPClient: httpClient, RetryMaxAttempts: 1}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("https://" + serviceHost + ":8333")
		o.UsePathStyle = false
	})
	return client, transport, nil
}

func hasCode(err error, codes ...string) bool {
	var api smithy.APIError
	if !errors.As(err, &api) {
		return false
	}
	for _, code := range codes {
		if api.ErrorCode() == code {
			return true
		}
	}
	return false
}

// A tiny object can be stored inline in filer metadata even when no data volume
// can accept writes. Use deterministic incompressible data above that threshold;
// check mode reproduces it without modifying the retained object.
func volumeProbePayload(run string) []byte {
	data := make([]byte, 64<<10)
	for offset := 0; offset < len(data); offset += sha256.Size {
		block := sha256.Sum256([]byte("apexload-volume-probe-v2:" + run + ":" + strconv.Itoa(offset)))
		copy(data[offset:], block[:])
	}
	return data
}

// check returns only fixed stage names; service errors may contain sensitive data.
func check(ctx context.Context, in inputs, mode string, client, bad *s3.Client) string {
	key := "fault/" + in.run + "/retention-probe-v2"
	payload := volumeProbePayload(in.run)
	if mode == "seed" {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
		if err != nil && !hasCode(err, "BucketAlreadyOwnedByYou", "BucketAlreadyExists") {
			return "create_bucket"
		}
		_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: &key, Body: bytes.NewReader(payload)})
		if err != nil {
			return "put_probe"
		}
	}
	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: &key})
	if err != nil {
		return "get_probe"
	}
	b, err := io.ReadAll(io.LimitReader(object.Body, int64(len(payload)+1)))
	object.Body.Close()
	if err != nil || !bytes.Equal(b, payload) {
		return "probe_contents"
	}
	// A service rejection is required. Transport/TLS failures cannot pass.
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("apexload-unowned")})
	if !hasCode(err, "AccessDenied") {
		return "bucket_scope"
	}
	object, err = bad.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: &key})
	if err == nil {
		object.Body.Close()
		return "credential_rejection"
	}
	if !hasCode(err, "SignatureDoesNotMatch", "AccessDenied") {
		return "credential_rejection"
	}
	return "complete"
}

func run(args []string) int {
	flags := flag.NewFlagSet("fault_s3_probe", flag.ContinueOnError)
	root := flags.String("inputs", "", "absolute private harness input directory")
	address := flags.String("address", "", "127.0.0.1:port of an explicitly prepared S3 port-forward")
	mode := flags.String("mode", "check", "seed writes the owned probe; check only reads it and checks access denials")
	output := flags.String("output", "", "new absolute private evidence directory")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() != 0 || (*mode != "seed" && *mode != "check") || !filepath.IsAbs(*output) {
		fmt.Fprintln(os.Stderr, "Invalid mode or output")
		return 2
	}
	in, err := loadInputs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid private inputs")
		return 2
	}
	client, transport, err := clientFor(in, *address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid private endpoint or trust")
		return 2
	}
	defer transport.CloseIdleConnections()
	badInputs := in
	badInputs.secret = "intentionally-invalid-secret"
	bad, badTransport, err := clientFor(badInputs, *address)
	if err != nil {
		return 2
	}
	defer badTransport.CloseIdleConnections()
	// Reserve evidence before issuing any writes. Never overwrite earlier results.
	if err = os.Mkdir(*output, 0700); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot reserve private evidence")
		return 2
	}
	f, err := os.OpenFile(filepath.Join(*output, "s3-evidence.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot reserve evidence file")
		return 2
	}
	report := evidence{RunID: in.run, Mode: *mode, StartedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	report.Stage = check(ctx, in, *mode, client, bad)
	report.Passed = report.Stage == "complete" && ctx.Err() == nil
	report.FinishedAt = time.Now().UTC()
	if !report.Passed && report.Stage == "complete" {
		report.Stage = "deadline"
	}
	err = json.NewEncoder(f).Encode(report)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		fmt.Fprintln(os.Stderr, "Cannot save evidence")
		return 1
	}
	if !report.Passed {
		fmt.Fprintln(os.Stderr, "S3 probe failed at "+report.Stage)
		return 1
	}
	fmt.Println("S3 " + *mode + " probe passed; private evidence saved")
	return 0
}

func main() { os.Exit(run(os.Args[1:])) }
