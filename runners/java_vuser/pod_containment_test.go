package java_vuser_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func podMust(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func podCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, e := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if e != nil {
		t.Fatalf("%s: %v %s", args[0], e, out)
	}
	return strings.TrimSpace(string(out))
}
func podDigest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func TestDedicatedPodDockerContainment(t *testing.T) {
	image := os.Getenv("APEXLOAD_JAVA_POD_DOCKER_IMAGE")
	if image == "" {
		t.Skip("set APEXLOAD_JAVA_POD_DOCKER_IMAGE to an already built local Alpine JDK21 image; no pull or install is performed")
	}
	for _, mode := range []string{"lease", "supervisor_failure"} {
		t.Run(mode, func(t *testing.T) { runPodDocker(t, image, mode) })
	}
}
func runPodDocker(t *testing.T, image, mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	for _, tool := range []string{"docker", "javac", "jar", "go"} {
		if _, e := exec.LookPath(tool); e != nil {
			t.Fatal(e)
		}
	}
	root, e := filepath.Abs("../..")
	podMust(t, e)
	image = podCommand(t, ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	arch := podCommand(t, ctx, "docker", "image", "inspect", "--format", "{{.Architecture}}", image)
	if arch != "arm64" && arch != "amd64" {
		t.Fatal("unsupported fixture image architecture", arch)
	}
	binary := filepath.Join(dir, "supervisor")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/apexload-java-host-supervisor")
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("supervisor build: %v %s", e, out)
	}
	classes := filepath.Join(dir, "classes")
	podMust(t, os.Mkdir(classes, 0755))
	_ = podCommand(t, ctx, "javac", "--release", "17", "-d", classes, filepath.Join(root, "runners/java_vuser/Harness.java"), filepath.Join(root, "runners/java_vuser/lrapi/lr.java"))
	_ = podCommand(t, ctx, "jar", "--create", "--file", filepath.Join(dir, "harness.jar"), "--main-class", "Harness", "-C", classes, ".")
	_ = podCommand(t, ctx, "javac", "--release", "17", "-cp", classes, "-d", dir, filepath.Join(root, "runners/java_vuser/evidence/PodContainmentActions.java"))
	_ = podCommand(t, ctx, "jar", "--create", "--file", filepath.Join(dir, "actions.jar"), "-C", dir, "PodContainmentActions.class")
	podMust(t, os.Chmod(dir, 0755))
	tlsDir := filepath.Join(dir, "tls")
	podMust(t, os.MkdirAll(tlsDir, 0755))
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	podMust(t, e)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local supervisor fixture"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &private.PublicKey, private)
	podMust(t, e)
	ca, e = x509.ParseCertificate(der)
	podMust(t, e)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	podMust(t, os.WriteFile(filepath.Join(tlsDir, "ca.crt"), caPEM, 0644))
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	cert := func(serial int64, identity string) (tls.Certificate, []byte, []byte) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		podMust(t, e)
		uri, e := url.Parse(identity)
		podMust(t, e)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "fixture"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, private)
		podMust(t, e)
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		podMust(t, e)
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	_, serverCert, serverKey := cert(2, "spiffe://apexload/java-host/evidence")
	podMust(t, os.WriteFile(filepath.Join(tlsDir, "tls.crt"), serverCert, 0644))
	podMust(t, os.WriteFile(filepath.Join(tlsDir, "tls.key"), serverKey, 0644))
	brokerCert, _, _ := cert(3, "spiffe://apexload/java-broker")
	var pulses atomic.Int64
	target, e := net.Listen("tcp", "0.0.0.0:0")
	podMust(t, e)
	defer target.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { pulses.Add(1); w.WriteHeader(204) }), ReadHeaderTimeout: time.Second}
	defer server.Close()
	go server.Serve(target)
	port := target.Addr().(*net.TCPAddr).Port
	javaHash := strings.Fields(podCommand(t, ctx, "docker", "run", "--pull=never", "--rm", "--network", "none", image, "sha256sum", "/usr/lib/jvm/java-21-openjdk/bin/java"))[0]
	versionOutput := podCommand(t, ctx, "docker", "run", "--pull=never", "--rm", "--network", "none", image, "java", "-version")
	t.Log("runtime:", versionOutput)
	versionParts := strings.Split(versionOutput, "\"")
	if len(versionParts) < 2 || !strings.HasPrefix(versionParts[1], "21.") {
		t.Fatal("fixture requires JDK21", versionOutput)
	}
	version := versionParts[1]
	harness, e := os.ReadFile(filepath.Join(dir, "harness.jar"))
	podMust(t, e)
	actions, e := os.ReadFile(filepath.Join(dir, "actions.jar"))
	podMust(t, e)
	runtime := javasidecar.DedicatedPodProfile{JDK: javarunner.ResolvedJDK{JavaBin: "/usr/lib/jvm/java-21-openjdk/bin/java", Home: "/usr/lib/jvm/java-21-openjdk", Version: version, Major: 21, VMFamily: javarunner.JavaVMHotSpot}, JavaSHA256: javaHash, Harness: javasidecar.PodAsset{Path: "/assets/harness.jar", SHA256: podDigest(harness)}, Classpath: []javasidecar.PodAsset{{Path: "/assets/actions.jar", SHA256: podDigest(actions)}}, VMParameters: []string{"-Xmx64m"}, Command: javasidecar.ActionCommand{ClassName: "PodContainmentActions", VUserID: "1"}}
	profile, e := json.Marshal(map[string]any{"contract": broker.SupervisorContract, "broker_client_uri": "spiffe://apexload/java-broker", "lease_ttl_ms": 10000, "preparation_timeout_ms": 30000, "invocation_timeout_ms": 30000, "runtime": runtime})
	podMust(t, e)
	podMust(t, os.WriteFile(filepath.Join(dir, "java-host.json"), profile, 0644))
	var unique [16]byte
	_, e = rand.Read(unique[:])
	podMust(t, e)
	name := "apex-java-supervisor-" + hex.EncodeToString(unique[:])
	cid := name
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, args := range [][]string{{"inspect", "--format", "{{json .State}}", cid}, {"logs", cid}, {"rm", "-f", cid}} {
			out, e := exec.CommandContext(cleanup, "docker", args...).CombinedOutput()
			t.Logf("docker %s: %s (%v)", args[0], out, e)
			if e != nil {
				t.Error("fixture cleanup/inspection failed")
			}
		}
	}()
	runArgs := []string{"docker", "run"}
	if goruntime.GOOS == "linux" {
		runArgs = append(runArgs, "--add-host", "host.docker.internal:host-gateway")
	}
	runArgs = append(runArgs, "--pull=never", "-d", "--name", name, "--label", "apexload.task=java-supervisor-evidence", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "10001:10001", "--memory", "256m", "--cpus", "1", "--pids-limit", "256", "--tmpfs", "/scratch:rw,size=32m,uid=10001,gid=10001", "--tmpfs", "/tmp:rw,size=32m,uid=10001,gid=10001", "--mount", "type=bind,src="+binary+",dst=/apexload-java-host-supervisor,readonly", "--mount", "type=bind,src="+dir+",dst=/assets,readonly", "--mount", "type=bind,src="+tlsDir+",dst=/transport,readonly", "-p", "127.0.0.1::9443", "--entrypoint", "/apexload-java-host-supervisor", image, "--host", "evidence", "--broker-epoch", "epoch", "--configuration-sha256", podDigest(profile))
	cid = podCommand(t, ctx, runArgs...)
	address := podCommand(t, ctx, "docker", "port", cid, "9443/tcp")
	// Docker's published TCP proxy can accept before the container listener
	// exists. Wait for a verified TLS handshake, not a TCP connect, before the
	// first stateful control exchange. Never replay an ambiguous invocation.
	startupDeadline := time.Now().Add(5 * time.Second)
	for {
		conn, e := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{brokerCert}})
		if e == nil {
			conn.Close()
			break
		}
		if ctx.Err() != nil || time.Now().After(startupDeadline) {
			t.Fatal("supervisor TLS did not start", e, ctx.Err())
		}
		time.Sleep(100 * time.Millisecond)
	}
	cfg := broker.SupervisorConfig{HostID: "evidence", BrokerEpoch: "epoch", ConfigurationSHA256: podDigest(profile), BrokerClientURI: "spiffe://apexload/java-broker", LeaseTTL: 10 * time.Second, PreparationTimeout: 30 * time.Second, InvocationTimeout: 30 * time.Second}
	client, e := broker.NewSupervisorClient("https://"+address, "spiffe://apexload/java-host/evidence", cfg, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{brokerCert}})
	podMust(t, e)
	defer client.Close()
	for {
		out, e := client.Control(ctx, "ready")
		podMust(t, e)
		if out.State == "ready" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if pulses.Load() != 0 {
		t.Fatal("traffic before commit")
	}
	_, e = client.Control(ctx, "commit")
	podMust(t, e)
	time.Sleep(500 * time.Millisecond)
	if pulses.Load() != 0 {
		t.Fatal("traffic before init")
	}
	response, e := client.Invoke(ctx, "init", map[string]string{"Target": fmt.Sprintf("http://host.docker.internal:%d/pulse", port)})
	podMust(t, e)
	var result javasidecar.ExecutionResponse
	podMust(t, json.Unmarshal(response.Payload, &result))
	if !result.Success {
		t.Fatalf("init: %+v", result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for pulses.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if pulses.Load() == 0 {
		t.Fatal("no descendant target traffic")
	}
	_, e = client.Control(ctx, "renew")
	podMust(t, e)
	start := time.Now()
	resultDone := make(chan error, 1)
	go func() { _, invokeErr := client.Invoke(ctx, "action", nil); resultDone <- invokeErr }()
	if mode == "supervisor_failure" {
		time.Sleep(300 * time.Millisecond)
		_ = podCommand(t, ctx, "docker", "kill", "--signal", "KILL", cid)
	}
	select {
	case e = <-resultDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if e == nil {
		t.Fatal("blocked Java unexpectedly completed")
	}
	status := podCommand(t, ctx, "docker", "wait", cid)
	stopped := pulses.Load()
	time.Sleep(time.Second)
	if pulses.Load() != stopped {
		t.Fatal("detached descendant survived PID1 exit")
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("lease/supervisor termination exceeded limit", time.Since(start))
	}
	t.Logf("PASS cold barrier and detached descendant cleanup; mode=%s exit=%s elapsed=%s pulses=%d", mode, status, time.Since(start), stopped)
}
