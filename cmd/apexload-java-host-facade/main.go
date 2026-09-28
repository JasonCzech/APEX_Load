// Experimental credential-minimized loopback facade. No Kubernetes credentials
// or infrastructure fencing authority belong in this process.
package main

import (
	"bytes"
	"context"
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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

var errConfig = errors.New("java_facade_configuration_invalid")

type facadeConfig struct {
	Contract       string `json:"contract"`
	Listen         string `json:"listen"`
	BrokerEndpoint string `json:"broker_endpoint"`
	BrokerURI      string `json:"broker_uri"`
	FacadeURI      string `json:"facade_uri"`
	Certificate    string `json:"certificate"`
	Key            string `json:"key"`
	CA             string `json:"ca"`
	TokenFile      string `json:"token_file"`
	InvocationMS   int64  `json:"invocation_ms"`
}

func readBounded(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errConfig
	}
	st, e := os.Stat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errConfig
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errConfig
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		return nil, errConfig
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, errConfig
	}
	return data, nil
}
func loadConfig(path string) (facadeConfig, error) {
	var c facadeConfig
	data, e := readBounded(path, 64<<10)
	if e != nil {
		return c, e
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || c.Contract != javasidecar.BrokerFacadeContract || c.InvocationMS < 1 || c.InvocationMS > 3600000 {
		return c, errConfig
	}
	host, port, e := net.SplitHostPort(c.Listen)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return c, errConfig
	}
	for _, p := range []string{c.Certificate, c.Key, c.CA, c.TokenFile} {
		if !filepath.IsAbs(p) {
			return c, errConfig
		}
	}
	return c, nil
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("java-host-facade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errConfig
	}
	cfg, e := loadConfig(*path)
	if e != nil {
		return e
	}
	return runConfigured(ctx, cfg, nil)
}
func runConfigured(ctx context.Context, c facadeConfig, onListening func(net.Addr)) error {
	cert, e := readBounded(c.Certificate, 128<<10)
	if e != nil {
		return e
	}
	key, e := readBounded(c.Key, 32<<10)
	if e != nil {
		return e
	}
	pair, e := tls.X509KeyPair(cert, key)
	if e != nil {
		return errConfig
	}
	ca, e := readBounded(c.CA, 128<<10)
	if e != nil {
		return e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errConfig
	}
	data, e := readBounded(c.TokenFile, 4096)
	if e != nil {
		return e
	}
	token := strings.TrimSpace(string(data))
	client, e := broker.NewBrokerClient(broker.BrokerClientConfig{Endpoint: c.BrokerEndpoint, ServerURI: c.BrokerURI, FacadeURI: c.FacadeURI, InvocationTimeout: time.Duration(c.InvocationMS) * time.Millisecond}, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{pair}})
	if e != nil {
		return errConfig
	}
	defer client.Close()
	facade, e := javasidecar.NewBrokerFacade(ctx, client, token)
	if e != nil {
		return e
	}
	defer facade.Close()
	listener, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return errConfig
	}
	server := &http.Server{Handler: facade, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, WriteTimeout: time.Duration(c.InvocationMS)*time.Millisecond + 10*time.Second, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if onListening != nil {
		onListening(listener.Addr())
	}
	select {
	case <-ctx.Done():
		// Closing connections cancels in-flight calls; facade.Close then revokes the
		// owner and waits for bounded cleanup requests, without claiming reaping.
		_ = server.Close()
		<-done
		return nil
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return errors.New("java_facade_listener_failed")
	}
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if run(ctx, os.Args[1:]) != nil {
		fmt.Fprintln(os.Stderr, "java_host_facade_stopped")
		os.Exit(1)
	}
}
