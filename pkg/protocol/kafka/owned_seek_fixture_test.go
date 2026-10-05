//go:build kafka_real && kafka_seek_integration

package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// This explicit qualification tag requires a genuine, disposable fixture. It
// never falls back to a mock broker or turns missing qualification into a skip.
type ownedSeekFixture struct {
	Endpoint       string   `json:"endpoint"`
	DialAddress    string   `json:"dial_address"`
	BrokerAddress  string   `json:"broker_address"`
	SetupAddress   string   `json:"setup_address"`
	CAFile         string   `json:"ca_file"`
	ProxyCertFile  string   `json:"proxy_cert_file"`
	ProxyKeyFile   string   `json:"proxy_key_file"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	OracleUsername string   `json:"oracle_username"`
	OraclePassword string   `json:"oracle_password"`
	Topic          string   `json:"topic"`
	UnpolledTopic  string   `json:"unpolled_topic"`
	Group          string   `json:"group"`
	PeerGroup      string   `json:"peer_group"`
	DeniedGroup    string   `json:"denied_group"`
	ClusterID      string   `json:"cluster_id"`
	Mechanism      string   `json:"mechanism,omitempty"`
	Mechanisms     []string `json:"mechanisms,omitempty"`
}

func loadOwnedSeekFixture(t *testing.T) (ownedSeekFixture, *tls.Config) {
	t.Helper()
	path := os.Getenv("APEXLOAD_KAFKA_SEEK_FIXTURE")
	if path == "" {
		t.Fatal("kafka_seek_integration requires APEXLOAD_KAFKA_SEEK_FIXTURE; genuine broker qualification is missing")
	}
	body := ownedSeekPrivateFile(t, path, 64<<10)
	defer clear(body)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var f ownedSeekFixture
	if decoder.Decode(&f) != nil {
		t.Fatal("seek fixture settings are invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("seek fixture settings contain trailing JSON")
	}
	endpoint, err := url.Parse(f.Endpoint)
	dial, de := netip.ParseAddrPort(f.DialAddress)
	broker, be := netip.ParseAddrPort(f.BrokerAddress)
	setup, se := netip.ParseAddrPort(f.SetupAddress)
	if err != nil || endpoint.Scheme != "tls" || endpoint.Hostname() != "localhost" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || de != nil || be != nil || se != nil || dial.Addr() != netip.MustParseAddr("127.0.0.1") || broker.Addr() != dial.Addr() || setup.Addr() != dial.Addr() || dial.Port() > 65533 || broker.Port() != dial.Port()+1 || setup.Port() != dial.Port()+2 || endpoint.Port() != strconv.Itoa(int(dial.Port())) {
		t.Fatal("seek fixture must use its exact allocated loopback routes")
	}
	for _, name := range []string{f.Topic, f.UnpolledTopic, f.Group, f.PeerGroup, f.DeniedGroup, f.ClusterID, f.Username, f.OracleUsername} {
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
			t.Fatal("seek fixture identity is missing or excessive")
		}
	}
	if f.Group == f.PeerGroup || f.Group == f.DeniedGroup || f.Topic == f.UnpolledTopic || f.Username == f.OracleUsername {
		t.Fatal("seek fixture identities must remain independent")
	}
	if f.Password == "" || f.OraclePassword == "" || len(f.Password) > 512 || len(f.OraclePassword) > 512 {
		t.Fatal("seek fixture credentials are missing or excessive")
	}
	if f.Mechanism == "" {
		f.Mechanism = "plain"
	}
	if !ownedSeekMechanismSupported(f.Mechanism) || len(f.Mechanisms) > 3 {
		t.Fatal("seek fixture authentication is unsupported")
	}
	for _, m := range f.Mechanisms {
		if !ownedSeekMechanismSupported(m) {
			t.Fatal("seek fixture authentication inventory is unsupported")
		}
	}
	for _, p := range []string{f.CAFile, f.ProxyCertFile, f.ProxyKeyFile} {
		if !filepath.IsAbs(p) || filepath.Dir(p) != filepath.Dir(path) {
			t.Fatal("seek fixture TLS files must belong to its private directory")
		}
	}
	ca := ownedSeekPrivateFile(t, f.CAFile, 512<<10)
	defer clear(ca)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("seek fixture trust is invalid")
	}
	if err := fileasset.RegisterRoot(filepath.Dir(path)); err != nil {
		t.Fatal("seek fixture private credential root unavailable")
	}
	return f, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: endpoint.Hostname()}
}
func ownedSeekPrivateFile(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("seek fixture private file unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() < 1 || info.Size() > limit {
		t.Fatal("seek fixture private file exceeds bounds or permissions")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) != info.Size() {
		clear(body)
		t.Fatal("seek fixture private file changed or is excessive")
	}
	return body
}
func ownedSeekMechanismSupported(m string) bool {
	return m == "plain" || m == "scram_sha_256" || m == "scram_sha_512"
}
func ownedSeekSASL(m, user, password string) sasl.Mechanism {
	switch m {
	case "scram_sha_256":
		return scram.Auth{User: user, Pass: password}.AsSha256Mechanism()
	case "scram_sha_512":
		return scram.Auth{User: user, Pass: password}.AsSha512Mechanism()
	default:
		return plain.Auth{User: user, Pass: password}.AsMechanism()
	}
}

// The execution proxy is intentionally separate from the read-only management
// proxy. It forwards real broker frames in wire order and can hold one exact
// ListOffsets response and one genuine primary rebalance heartbeat response.
// It never fabricates assignments, offsets, fetches or responses.
const ownedSeekFrameLimit = 8 << 20
const ownedSeekProxyBytes = 128 << 20
const ownedSeekProxyFrames = 20000
const ownedSeekProxyConnections = 32
const ownedSeekProxyInFlight = 8

type ownedSeekHeldResponse struct {
	entered, release chan struct{}
	once             sync.Once
	released         bool
	kind             string
	deadline         time.Time
}

func (h *ownedSeekHeldResponse) Release() { h.once.Do(func() { close(h.release) }) }

type ownedSeekProxy struct {
	ctx                                  context.Context
	cancel                               context.CancelFunc
	listener                             net.Listener
	tls, upstreamTLS                     *tls.Config
	broker                               string
	mu                                   sync.Mutex
	closing                              bool
	sockets                              map[net.Conn]bool
	wg                                   sync.WaitGroup
	accepts, active, peak, frames, bytes int
	counts                               map[int16]int
	failures                             []string
	armed, heartbeat                     *ownedSeekHeldResponse
	holds                                []*ownedSeekHeldResponse
	topic, group                         string
	pipelinePeak                         int
}

func newOwnedSeekProxy(t *testing.T, ctx context.Context, f ownedSeekFixture, upstreamTLS *tls.Config) *ownedSeekProxy {
	t.Helper()
	certBody := ownedSeekPrivateFile(t, f.ProxyCertFile, 512<<10)
	defer clear(certBody)
	keyBody := ownedSeekPrivateFile(t, f.ProxyKeyFile, 512<<10)
	defer clear(keyBody)
	cert, err := tls.X509KeyPair(certBody, keyBody)
	if err != nil {
		t.Fatal("seek fixture proxy certificate unavailable")
	}
	listener, err := net.Listen("tcp4", f.DialAddress)
	if err != nil {
		t.Fatal("seek fixture execution proxy could not bind")
	}
	pctx, cancel := context.WithCancel(ctx)
	p := &ownedSeekProxy{ctx: pctx, cancel: cancel, listener: listener, tls: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, upstreamTLS: upstreamTLS.Clone(), broker: f.BrokerAddress, sockets: map[net.Conn]bool{}, counts: map[int16]int{}, topic: f.Topic, group: f.Group}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.closing {
				p.mu.Unlock()
				raw.Close()
				return
			}
			if p.accepts >= ownedSeekProxyConnections {
				p.mu.Unlock()
				raw.Close()
				p.fail("execution proxy connection budget exceeded")
				return
			}
			p.accepts++
			p.active++
			p.peak = max(p.peak, p.active)
			p.sockets[raw] = true
			p.wg.Add(1)
			p.mu.Unlock()
			go p.forward(raw)
		}
	}()
	t.Cleanup(func() { p.Close(t) })
	return p
}
func (p *ownedSeekProxy) fail(reason string) {
	p.mu.Lock()
	if len(p.failures) < 8 {
		p.failures = append(p.failures, reason)
	}
	p.mu.Unlock()
}
func (p *ownedSeekProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		conn.Close()
		return false
	}
	p.sockets[conn] = true
	return true
}
func (p *ownedSeekProxy) untrack(conn net.Conn) {
	conn.Close()
	p.mu.Lock()
	delete(p.sockets, conn)
	p.mu.Unlock()
}
func (p *ownedSeekProxy) forward(raw net.Conn) {
	defer p.wg.Done()
	defer func() { p.untrack(raw); p.mu.Lock(); p.active--; p.mu.Unlock() }()
	deadline, ok := p.ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Minute)
	}
	raw.SetDeadline(deadline)
	client := tls.Server(raw, p.tls)
	if client.HandshakeContext(p.ctx) != nil {
		return
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}
	upstreamRaw, err := dialer.DialContext(p.ctx, "tcp4", p.broker)
	if err != nil {
		if p.ctx.Err() == nil {
			p.fail("execution proxy broker dial failed")
		}
		return
	}
	if !p.track(upstreamRaw) {
		return
	}
	defer p.untrack(upstreamRaw)
	upstreamRaw.SetDeadline(deadline)
	upstream := tls.Client(upstreamRaw, p.upstreamTLS.Clone())
	if upstream.HandshakeContext(p.ctx) != nil {
		if p.ctx.Err() == nil {
			p.fail("execution proxy upstream TLS failed")
		}
		return
	}
	p.runPipeline(p.ctx, client, upstream)
}

// A broker's normal connection carries both ListOffsets and heartbeats. Read
// ahead remains bounded, while writes preserve the broker's exact response
// order. This lets an earlier real rebalance heartbeat reach the SDK while a
// following lookup is held; a serial request/response proxy would block both.
type ownedSeekWireRequest struct {
	key, version     int16
	correlation      int32
	primaryHeartbeat bool
	lookup           *ownedSeekHeldResponse
}
type ownedSeekWireResponse struct {
	body []byte
	held *ownedSeekHeldResponse
}

func (p *ownedSeekProxy) runPipeline(parent context.Context, client, upstream net.Conn) {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	requests := make(chan ownedSeekWireRequest, ownedSeekProxyInFlight)
	responses := make(chan ownedSeekWireResponse, ownedSeekProxyInFlight)
	slots := make(chan struct{}, ownedSeekProxyInFlight)
	stop := func() { cancel(); ownedSeekCloseTransport(client); ownedSeekCloseTransport(upstream) }
	watcherExited := make(chan struct{})
	stopWatcher := context.AfterFunc(ctx, func() { defer close(watcherExited); stop() })
	defer func() {
		stop()
		if !stopWatcher() {
			<-watcherExited
		}
		workers.Wait()
		for {
			select {
			case response := <-responses:
				clear(response.body)
			default:
				return
			}
		}
	}()
	workers.Add(2)
	go func() {
		defer workers.Done()
		defer stop()
		for {
			var request ownedSeekWireRequest
			select {
			case request = <-requests:
			case <-ctx.Done():
				return
			}
			response, err := ownedSeekReadFrame(upstream)
			if err != nil {
				if ctx.Err() == nil {
					p.fail("execution proxy upstream response failed")
				}
				return
			}
			if len(response) < 4 || int32(binary.BigEndian.Uint32(response[:4])) != request.correlation {
				clear(response)
				p.fail("execution proxy response correlation failed")
				return
			}
			p.mu.Lock()
			p.bytes += len(response) + 4
			within := p.bytes <= ownedSeekProxyBytes
			p.mu.Unlock()
			if !within {
				clear(response)
				p.fail("execution proxy response budget exceeded")
				return
			}
			held := request.lookup
			if held == nil {
				held = p.captureHeartbeatResponse(request, response)
			}
			if held != nil {
				held.deadline = time.Now().Add(8 * time.Second)
				close(held.entered)
			}
			select {
			case responses <- ownedSeekWireResponse{body: response, held: held}:
			case <-ctx.Done():
				clear(response)
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer stop()
		for {
			var response ownedSeekWireResponse
			select {
			case response = <-responses:
			case <-ctx.Done():
				return
			}
			if response.held != nil && !p.awaitHeld(ctx, response.held) {
				clear(response.body)
				return
			}
			err := ownedSeekWriteFrame(client, response.body)
			clear(response.body)
			if err != nil {
				return
			}
			<-slots
		}
	}()
	for {
		body, err := ownedSeekReadFrame(client)
		if err != nil {
			return
		}
		if len(body) < 10 {
			clear(body)
			p.fail("execution proxy request header truncated")
			return
		}
		key := int16(binary.BigEndian.Uint16(body[:2]))
		version := int16(binary.BigEndian.Uint16(body[2:4]))
		request := ownedSeekWireRequest{key: key, version: version, correlation: int32(binary.BigEndian.Uint32(body[4:8]))}
		if !ownedSeekExecutionKey(key) {
			clear(body)
			p.fail("execution proxy observed an unexpected API")
			return
		}
		req := kmsg.RequestForKey(key)
		if req == nil || version < 0 || version > req.MaxVersion() {
			clear(body)
			p.fail("execution proxy observed an unexpected version")
			return
		}
		p.mu.Lock()
		p.frames++
		p.bytes += len(body) + 4
		p.counts[key]++
		within := p.frames <= ownedSeekProxyFrames && p.bytes <= ownedSeekProxyBytes
		p.mu.Unlock()
		if !within {
			clear(body)
			p.fail("execution proxy request budget exceeded")
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			clear(body)
			p.fail("execution proxy in-flight request budget exceeded")
			return
		}
		p.mu.Lock()
		p.pipelinePeak = max(p.pipelinePeak, len(slots))
		p.mu.Unlock()
		request.lookup = p.captureHold(body, key, version)
		request.primaryHeartbeat = p.primaryHeartbeatRequest(body, req)
		select {
		case requests <- request:
		case <-ctx.Done():
			clear(body)
			return
		}
		err = ownedSeekWriteFrame(upstream, body)
		clear(body)
		if err != nil {
			if ctx.Err() == nil {
				p.fail("execution proxy upstream write failed")
			}
			return
		}
	}
}

func ownedSeekCloseTransport(conn net.Conn) {
	// TLS Close may wait to send close_notify; fixture cancellation owns the
	// underlying socket and must unblock every reader/writer immediately.
	if secure, ok := conn.(*tls.Conn); ok {
		secure.NetConn().Close()
		return
	}
	conn.Close()
}

func (p *ownedSeekProxy) awaitHeld(ctx context.Context, held *ownedSeekHeldResponse) bool {
	if ctx.Err() != nil {
		return false
	}
	if !time.Now().Before(held.deadline) {
		p.fail("execution proxy held response exceeded bounded release")
		return false
	}
	timer := time.NewTimer(time.Until(held.deadline))
	defer timer.Stop()
	select {
	case <-held.release:
		if ctx.Err() != nil {
			return false
		}
		if !time.Now().Before(held.deadline) {
			p.fail("execution proxy held response exceeded bounded release")
			return false
		}
		held.released = true
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		if ctx.Err() == nil {
			p.fail("execution proxy held response exceeded bounded release")
		}
		return false
	}
}

func ownedSeekExecutionKey(key int16) bool {
	switch key {
	case 0, 1, 2, 3, 8, 9, 10, 11, 12, 13, 14, 15, 17, 18, 22, 23, 36:
		return true
	}
	return false
}
func ownedSeekPayload(body []byte, headerBytes int, flexible bool) ([]byte, bool) {
	if headerBytes < 0 || headerBytes > len(body) {
		return nil, false
	}
	offset := headerBytes
	if flexible {
		tags, n := binary.Uvarint(body[offset:])
		if n <= 0 || tags > 8 {
			return nil, false
		}
		offset += n
		for range tags {
			_, n = binary.Uvarint(body[offset:])
			if n <= 0 {
				return nil, false
			}
			offset += n
			size, n := binary.Uvarint(body[offset:])
			if n <= 0 || size > uint64(len(body)-offset-n) {
				return nil, false
			}
			offset += n + int(size)
		}
	}
	return body[offset:], true
}
func ownedSeekRequestPayload(body []byte, req kmsg.Request) ([]byte, string, bool) {
	if len(body) < 10 {
		return nil, "", false
	}
	size := int(int16(binary.BigEndian.Uint16(body[8:10])))
	if size < -1 || size > 256 || size > len(body)-10 {
		return nil, "", false
	}
	clientID := ""
	if size >= 0 {
		clientID = string(body[10 : 10+size])
	} else {
		size = 0
	}
	payload, ok := ownedSeekPayload(body, 10+size, req.IsFlexible())
	return payload, clientID, ok
}
func (p *ownedSeekProxy) primaryHeartbeatRequest(body []byte, generic kmsg.Request) bool {
	if len(body) < 10 || generic.Key() != 12 {
		return false
	}
	req := kmsg.NewPtrHeartbeatRequest()
	req.SetVersion(int16(binary.BigEndian.Uint16(body[2:4])))
	payload, clientID, ok := ownedSeekRequestPayload(body, req)
	return ok && clientID == "apex-owned-seek-primary" && req.ReadFrom(payload) == nil && req.Group == p.group
}
func (p *ownedSeekProxy) captureHeartbeatResponse(request ownedSeekWireRequest, body []byte) *ownedSeekHeldResponse {
	if !request.primaryHeartbeat {
		return nil
	}
	response := kmsg.NewPtrHeartbeatResponse()
	response.SetVersion(request.version)
	payload, ok := ownedSeekPayload(body, 4, response.IsFlexible())
	if !ok || response.ReadFrom(payload) != nil || response.ErrorCode != 27 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	held := p.heartbeat
	p.heartbeat = nil
	return held
}
func (p *ownedSeekProxy) captureHold(body []byte, key, version int16) *ownedSeekHeldResponse {
	if key != 2 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.armed == nil {
		return nil
	}
	req := kmsg.NewPtrListOffsetsRequest()
	req.SetVersion(version)
	payload, clientID, ok := ownedSeekRequestPayload(body, req)
	if !ok || clientID != "apex-owned-seek-primary" || req.ReadFrom(payload) != nil || len(req.Topics) != 1 || req.Topics[0].Topic != p.topic || len(req.Topics[0].Partitions) != 3 {
		return nil
	}
	seen := map[int32]bool{}
	for _, part := range req.Topics[0].Partitions {
		if part.Partition < 0 || part.Partition > 2 || seen[part.Partition] || part.Timestamp != -2 {
			return nil
		}
		seen[part.Partition] = true
	}
	h := p.armed
	p.armed = nil
	return h
}
func (p *ownedSeekProxy) HoldEarliest(t *testing.T) *ownedSeekHeldResponse {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, h := range p.holds {
		if h.kind == "lookup" {
			t.Fatal("execution proxy permits exactly one held lookup")
		}
	}
	h := &ownedSeekHeldResponse{entered: make(chan struct{}), release: make(chan struct{}), kind: "lookup"}
	p.armed = h
	p.holds = append(p.holds, h)
	return h
}
func (p *ownedSeekProxy) HoldRebalanceHeartbeat(t *testing.T) *ownedSeekHeldResponse {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, h := range p.holds {
		if h.kind == "rebalance heartbeat" {
			t.Fatal("execution proxy permits exactly one held rebalance heartbeat")
		}
	}
	h := &ownedSeekHeldResponse{entered: make(chan struct{}), release: make(chan struct{}), kind: "rebalance heartbeat"}
	p.heartbeat = h
	p.holds = append(p.holds, h)
	return h
}
func ownedSeekReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := int64(int32(binary.BigEndian.Uint32(header[:])))
	if size < 4 || size > ownedSeekFrameLimit {
		return nil, errors.New("execution proxy frame exceeds bounds")
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(r, body); err != nil {
		clear(body)
		return nil, err
	}
	return body, nil
}
func ownedSeekWriteFrame(w io.Writer, body []byte) error {
	header := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	for _, part := range [][]byte{header, body} {
		for len(part) > 0 {
			n, err := w.Write(part)
			if err != nil {
				return err
			}
			if n < 1 || n > len(part) {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func (p *ownedSeekProxy) Counts() map[int16]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[int16]int{}
	for k, v := range p.counts {
		out[k] = v
	}
	return out
}
func (p *ownedSeekProxy) Close(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.closing = true
	p.cancel()
	p.listener.Close()
	for _, h := range p.holds {
		h.Release()
	}
	for conn := range p.sockets {
		conn.Close()
	}
	p.mu.Unlock()
	joined := make(chan struct{})
	go func() { p.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Error("execution proxy owned handlers did not physically join")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != 0 || len(p.sockets) != 0 {
		t.Error("execution proxy sockets remain owned")
	}
	if len(p.failures) != 0 {
		t.Errorf("execution proxy failure classes: %v", p.failures)
	}
	for _, h := range p.holds {
		select {
		case <-h.entered:
			if !h.released {
				t.Errorf("held %s never reached its release proof", h.kind)
			}
		default:
			t.Errorf("armed genuine %s was never observed", h.kind)
		}
	}
	t.Logf("genuine execution proxy joined: accepts=%d peak=%d frames=%d bytes=%d API counts=%v pipeline peak=%d", p.accepts, p.peak, p.frames, p.bytes, p.counts, p.pipelinePeak)
}

// This oracle never configures ConsumerGroup. Its independently authenticated
// ACLs and wire-key audit forbid fetch/produce/join/commit as well as setup I/O.
type ownedSeekOracleAudit struct {
	mu        sync.Mutex
	keys      map[int16]int
	forbidden bool
}

func (a *ownedSeekOracleAudit) OnBrokerWrite(_ kgo.BrokerMetadata, key int16, _ int, _ time.Duration, _ time.Duration, _ error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[key]++
	switch key {
	case 3, 9, 10, 15, 17, 18, 36:
	default:
		a.forbidden = true
	}
}

var _ kgo.HookBrokerWrite = (*ownedSeekOracleAudit)(nil)

type ownedSeekOracle struct {
	cl    *kgo.Client
	audit *ownedSeekOracleAudit
}

func ownedSeekOracleOptions(ctx context.Context, f ownedSeekFixture, tlsConfig *tls.Config, audit *ownedSeekOracleAudit) []kgo.Opt {
	endpoint, _ := url.Parse(f.Endpoint)
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != endpoint.Host {
			return nil, errors.New("oracle advertised address is outside its exact fixture route")
		}
		return (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}, Config: tlsConfig.Clone()}).DialContext(ctx, "tcp4", f.BrokerAddress)
	}
	return []kgo.Opt{
		kgo.WithContext(ctx), kgo.SeedBrokers(endpoint.Host), kgo.Dialer(dial),
		kgo.SASL(ownedSeekSASL(f.Mechanism, f.OracleUsername, f.OraclePassword)),
		kgo.WithHooks(audit), kgo.ClientID("apex-owned-seek-oracle"),
		kgo.RequestRetries(2), kgo.RetryTimeout(3 * time.Second), kgo.RequestTimeoutOverhead(time.Second),
		kgo.BrokerMaxReadBytes(1 << 20), kgo.BrokerMaxWriteBytes(1 << 20),
		// The SDK validates its default fetch limit against the read bound even
		// for clients without ConsumerGroup or ConsumeTopics. Match that inert
		// setting to the bound rather than increasing the oracle's read budget.
		kgo.FetchMaxBytes(1 << 20),
	}
}

func newOwnedSeekOracle(t *testing.T, ctx context.Context, f ownedSeekFixture, tlsConfig *tls.Config) *ownedSeekOracle {
	t.Helper()
	audit := &ownedSeekOracleAudit{keys: map[int16]int{}}
	cl, err := kgo.NewClient(ownedSeekOracleOptions(ctx, f, tlsConfig, audit)...)
	if err != nil {
		t.Fatal("independent seek oracle construction failed")
	}
	o := &ownedSeekOracle{cl: cl, audit: audit}
	t.Cleanup(func() {
		cl.Close()
		audit.mu.Lock()
		defer audit.mu.Unlock()
		if audit.forbidden {
			t.Error("independent seek oracle issued a mutation/group-joining request")
		}
		t.Logf("independent direct oracle API counts=%v", audit.keys)
	})
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	req.Topics = []kmsg.MetadataRequestTopic{{Topic: &f.Topic}, {Topic: &f.UnpolledTopic}}
	response, err := cl.Request(ctx, req)
	if err != nil {
		t.Fatal("independent seek oracle metadata unavailable")
	}
	metadata, ok := response.(*kmsg.MetadataResponse)
	if !ok || metadata.ClusterID == nil || *metadata.ClusterID != f.ClusterID || len(metadata.Topics) != 2 {
		t.Fatal("independent seek oracle cluster/topic identity mismatch")
	}
	for _, topic := range metadata.Topics {
		if topic.ErrorCode != 0 || topic.Topic == nil || (*topic.Topic != f.Topic && *topic.Topic != f.UnpolledTopic) || len(topic.Partitions) != 3 {
			t.Fatal("independent seek oracle topic inventory mismatch")
		}
		for _, partition := range topic.Partitions {
			if partition.ErrorCode != 0 || partition.Partition < 0 || partition.Partition > 2 {
				t.Fatal("independent seek oracle partition inventory mismatch")
			}
		}
	}
	return o
}
func (o *ownedSeekOracle) Offsets(t *testing.T, ctx context.Context, group, topic string) map[int32]int64 {
	t.Helper()
	req := kmsg.NewPtrOffsetFetchRequest()
	req.Group = group
	req.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: []int32{0, 1, 2}}}
	offsetGroup := kmsg.NewOffsetFetchRequestGroup()
	offsetGroup.Group = group
	offsetGroup.Topics = []kmsg.OffsetFetchRequestGroupTopic{{Topic: topic, Partitions: []int32{0, 1, 2}}}
	req.Groups = []kmsg.OffsetFetchRequestGroup{offsetGroup}
	resp, retries, transientCode, err := ownedSeekReadOffsetFetch(ctx, func(ctx context.Context) (kmsg.Response, error) {
		return o.cl.Request(ctx, req)
	})
	if retries > 0 {
		t.Logf("independent real coordinator request retried %d times after Kafka code %d", retries, transientCode)
	}
	if err != nil {
		t.Fatalf("independent committed-offset oracle failed (class=%s)", ownedSeekOracleFailureClass(err))
	}
	r, ok := resp.(*kmsg.OffsetFetchResponse)
	if !ok || r.ErrorCode != 0 {
		t.Fatal("independent committed-offset response invalid")
	}
	result := map[int32]int64{}
	add := func(name string, partition int32, offset int64, code int16) {
		if name != topic || partition < 0 || partition > 2 || code != 0 {
			t.Fatalf("independent committed-offset response rejected (code=%d)", code)
		}
		if _, exists := result[partition]; exists {
			t.Fatal("independent committed-offset response duplicated partition")
		}
		result[partition] = offset
	}
	if r.Version >= 8 {
		if len(r.Groups) != 1 || r.Groups[0].Group != group || r.Groups[0].ErrorCode != 0 {
			t.Fatal("independent committed-offset group mismatch")
		}
		for _, tp := range r.Groups[0].Topics {
			for _, p := range tp.Partitions {
				add(tp.Topic, p.Partition, p.Offset, p.ErrorCode)
			}
		}
	} else {
		for _, tp := range r.Topics {
			for _, p := range tp.Partitions {
				add(tp.Topic, p.Partition, p.Offset, p.ErrorCode)
			}
		}
	}
	if len(result) != 3 {
		t.Fatal("independent committed-offset response incomplete")
	}
	return result
}
func (o *ownedSeekOracle) AssertOffsets(t *testing.T, ctx context.Context, group, topic string, want map[int32]int64) {
	t.Helper()
	got := o.Offsets(t, ctx, group, topic)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("broker committed offsets changed: got=%v want=%v", got, want)
	}
}

// This offline regression constructs the same real SDK options as the broker
// oracle. It is configuration evidence, not a substitute for live qualification.
func TestOwnedSeekOracleBoundedConstruction(t *testing.T) {
	legacy, err := kgo.NewClient(kgo.SeedBrokers("localhost:19392"), kgo.BrokerMaxReadBytes(1<<20))
	if err == nil {
		legacy.Close()
		t.Fatal("pinned SDK no longer rejects the default fetch/read bound mismatch")
	}
	if !strings.Contains(err.Error(), "max fetch bytes") {
		t.Fatal("pinned SDK rejected the legacy bound for an unrelated reason")
	}
	audit := &ownedSeekOracleAudit{keys: map[int16]int{}}
	settings := ownedSeekFixture{
		Endpoint: "tls://localhost:19392", BrokerAddress: "127.0.0.1:19393",
		Mechanism: "plain", OracleUsername: "oracle-unit", OraclePassword: "test-only-password",
	}
	opts := ownedSeekOracleOptions(t.Context(), settings, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost"}, audit)
	// Construction needs no broker; forbid all actual network use in this unit
	// regression while exercising SDK validation of the unchanged oracle options.
	opts = append(opts, kgo.Dialer(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("offline oracle construction forbids network")
	}))
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal("bounded read-only oracle options failed real SDK construction")
	}
	t.Cleanup(cl.Close)
	for _, opt := range []any{kgo.BrokerMaxReadBytes, kgo.BrokerMaxWriteBytes, kgo.FetchMaxBytes} {
		if value, ok := cl.OptValue(opt).(int32); !ok || value != 1<<20 {
			t.Fatal("oracle read/write/fetch budget changed")
		}
	}
	if cl.OptValue(kgo.ConsumerGroup) != "" {
		t.Fatal("oracle construction configured a consumer group")
	}
	if topics := reflect.ValueOf(cl.OptValue(kgo.ConsumeTopics)); topics.Kind() != reflect.Map || topics.Len() != 0 {
		t.Fatal("oracle construction configured a topic consumer")
	}
}

// A fresh broker may still be bringing its internal offsets coordinator online.
// Retry only the three coordinator transient codes; authorization, routing and
// unknown failures remain immediate. No oracle mutation is used to prime it.
func ownedSeekReadOffsetFetch(ctx context.Context, request func(context.Context) (kmsg.Response, error)) (response kmsg.Response, retries int, transientCode int16, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if err = ctx.Err(); err != nil {
			return nil, retries, transientCode, err
		}
		response, err = request(ctx)
		if err == nil {
			if err = ctx.Err(); err != nil {
				return nil, retries, transientCode, err
			}
			return response, retries, transientCode, nil
		}
		var kafkaError *kerr.Error
		if !errors.As(err, &kafkaError) || kafkaError.Code != 14 && kafkaError.Code != 15 && kafkaError.Code != 16 {
			return nil, retries, transientCode, err
		}
		transientCode = kafkaError.Code
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, retries, transientCode, ctx.Err()
		case <-timer.C:
			retries++
		}
	}
}

// Error text from network/SASL libraries can include credential material. Only
// closed categories and numeric broker codes may enter retained fixture logs.
func ownedSeekOracleFailureClass(err error) string {
	var kafkaError *kerr.Error
	switch {
	case errors.As(err, &kafkaError):
		return "kafka-code-" + strconv.Itoa(int(kafkaError.Code))
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "unclassified"
	}
}

// Callback isolation tests admission/retry policy only. Genuine coordinator and
// committed-offset evidence still requires TestKafkaOwnedSeekActualBroker.
func TestOwnedSeekOracleReadRetriesOnlyCoordinatorTransients(t *testing.T) {
	for _, transient := range []error{kerr.CoordinatorLoadInProgress, kerr.CoordinatorNotAvailable, kerr.NotCoordinator} {
		t.Run(ownedSeekOracleFailureClass(transient), func(t *testing.T) {
			calls := 0
			want := kmsg.NewPtrOffsetFetchResponse()
			got, retries, code, err := ownedSeekReadOffsetFetch(t.Context(), func(context.Context) (kmsg.Response, error) {
				calls++
				if calls == 1 {
					return nil, transient
				}
				return want, nil
			})
			if err != nil || got != want || calls != 2 || retries != 1 || code < 14 || code > 16 {
				t.Fatal("known coordinator transient was not retried exactly once")
			}
		})
	}
	for _, refusal := range []error{kerr.GroupAuthorizationFailed, kerr.TopicAuthorizationFailed, kerr.SaslAuthenticationFailed, kerr.UnknownServerError, errors.New("oracle advertised address is outside its exact fixture route"), errors.New("private credential must never appear in logs")} {
		t.Run(ownedSeekOracleFailureClass(refusal), func(t *testing.T) {
			calls := 0
			_, retries, _, err := ownedSeekReadOffsetFetch(t.Context(), func(context.Context) (kmsg.Response, error) {
				calls++
				return nil, refusal
			})
			if err != refusal || calls != 1 || retries != 0 {
				t.Fatal("nontransient oracle failure retried or disappeared")
			}
			if strings.Contains(ownedSeekOracleFailureClass(err), "private") {
				t.Fatal("oracle failure classification disclosed error text")
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
		defer cancel()
		calls := 0
		_, retries, code, err := ownedSeekReadOffsetFetch(ctx, func(context.Context) (kmsg.Response, error) {
			calls++
			return nil, kerr.CoordinatorNotAvailable
		})
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || retries != 0 || code != 15 || ownedSeekOracleFailureClass(err) != "deadline" {
			t.Fatal("coordinator retry crossed bounded read deadline")
		}
	})
	t.Run("cancelled-after-response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		got, retries, _, err := ownedSeekReadOffsetFetch(ctx, func(context.Context) (kmsg.Response, error) {
			cancel()
			return kmsg.NewPtrOffsetFetchResponse(), nil
		})
		if got != nil || !errors.Is(err, context.Canceled) || retries != 0 {
			t.Fatal("cancelled oracle admitted a late response")
		}
	})
	t.Run("cancelled-before-request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, retries, _, err := ownedSeekReadOffsetFetch(ctx, func(context.Context) (kmsg.Response, error) {
			t.Fatal("cancelled oracle issued another request")
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) || retries != 0 || ownedSeekOracleFailureClass(err) != "cancelled" {
			t.Fatal("oracle ignored pre-request cancellation")
		}
	})
}
