//go:build kafka_real && kafka_transaction_integration

package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
)

// Explicit qualification has no mock/skip fallback. Three distinct principals
// independently write fixture records, observe positions, and read output.
type transactionFixture struct {
	Endpoint            string `json:"endpoint"`
	DialAddress         string `json:"dial_address"`
	BrokerAddress       string `json:"broker_address"`
	SetupAddress        string `json:"setup_address"`
	CAFile              string `json:"ca_file"`
	ProxyCertFile       string `json:"proxy_cert_file"`
	ProxyKeyFile        string `json:"proxy_key_file"`
	ClusterID           string `json:"cluster_id"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	OracleUsername      string `json:"oracle_username"`
	OraclePassword      string `json:"oracle_password"`
	WriterUsername      string `json:"writer_username"`
	WriterPassword      string `json:"writer_password"`
	OutputUsername      string `json:"output_username"`
	OutputPassword      string `json:"output_password"`
	VisibilityTopic     string `json:"visibility_topic"`
	InputTopic          string `json:"input_topic"`
	OutputTopic         string `json:"output_topic"`
	Group               string `json:"group"`
	SeekGroup           string `json:"seek_group"`
	FenceGroup          string `json:"fence_group"`
	TransactionalPrefix string `json:"transactional_prefix"`
}

func loadTransactionFixture(t *testing.T) (transactionFixture, *tls.Config) {
	t.Helper()
	path := os.Getenv("APEXLOAD_KAFKA_TRANSACTION_FIXTURE")
	if path == "" {
		t.Fatal("kafka_transaction_integration requires APEXLOAD_KAFKA_TRANSACTION_FIXTURE; genuine qualification is missing")
	}
	body := ownedSeekPrivateFile(t, path, 64<<10)
	defer clear(body)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var f transactionFixture
	if decoder.Decode(&f) != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatal("transaction fixture settings invalid")
	}
	endpoint, err := url.Parse(f.Endpoint)
	dial, de := netip.ParseAddrPort(f.DialAddress)
	broker, be := netip.ParseAddrPort(f.BrokerAddress)
	setup, se := netip.ParseAddrPort(f.SetupAddress)
	if err != nil || endpoint.Scheme != "tls" || endpoint.Hostname() != "localhost" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || de != nil || be != nil || se != nil || dial.Addr() != netip.MustParseAddr("127.0.0.1") || broker.Addr() != dial.Addr() || setup.Addr() != dial.Addr() || dial.Port() > 65533 || broker.Port() != dial.Port()+1 || setup.Port() != dial.Port()+2 || endpoint.Port() != strconv.Itoa(int(dial.Port())) {
		t.Fatal("transaction fixture routes must be exact allocated loopback endpoints")
	}
	seen := map[string]bool{}
	for _, name := range []string{f.VisibilityTopic, f.InputTopic, f.OutputTopic, f.Group, f.SeekGroup, f.FenceGroup, f.Username, f.WriterUsername, f.OracleUsername, f.OutputUsername} {
		if name == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") || seen[name] {
			t.Fatal("transaction fixture identities must be bounded and distinct")
		}
		seen[name] = true
	}
	if f.ClusterID == "" || len(f.ClusterID) > 128 || f.TransactionalPrefix == "" || len(f.TransactionalPrefix) > 96 || !strings.HasPrefix(f.TransactionalPrefix, "apex-txn-") || !strings.HasSuffix(f.TransactionalPrefix, "-") {
		t.Fatal("transaction fixture scope identity invalid")
	}
	for _, password := range []string{f.Password, f.WriterPassword, f.OraclePassword, f.OutputPassword} {
		if password == "" || len(password) > 512 {
			t.Fatal("transaction fixture credential missing or excessive")
		}
	}
	for _, p := range []string{f.CAFile, f.ProxyCertFile, f.ProxyKeyFile} {
		if !filepath.IsAbs(p) || filepath.Dir(p) != filepath.Dir(path) {
			t.Fatal("transaction fixture trust must be in its protected directory")
		}
	}
	ca := ownedSeekPrivateFile(t, f.CAFile, 512<<10)
	defer clear(ca)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("transaction fixture trust invalid")
	}
	if fileasset.RegisterRoot(filepath.Dir(path)) != nil {
		t.Fatal("transaction fixture credential root unavailable")
	}
	return f, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost"}
}

func (f transactionFixture) transport() ownedSeekFixture {
	return ownedSeekFixture{Endpoint: f.Endpoint, DialAddress: f.DialAddress, BrokerAddress: f.BrokerAddress, SetupAddress: f.SetupAddress, CAFile: f.CAFile, ProxyCertFile: f.ProxyCertFile, ProxyKeyFile: f.ProxyKeyFile, Topic: f.VisibilityTopic, Group: f.SeekGroup}
}

// Each independent client is routed directly to the one broker TLS socket.
// Advertised metadata cannot route it through execution or setup listeners.
func transactionDirectOptions(ctx context.Context, f transactionFixture, trust *tls.Config, user, password, id string) []kgo.Opt {
	endpoint, _ := url.Parse(f.Endpoint)
	dial := func(ctx context.Context, _ string, address string) (net.Conn, error) {
		if address != endpoint.Host {
			return nil, errors.New("transaction fixture advertised route refused")
		}
		return (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}, Config: trust.Clone()}).DialContext(ctx, "tcp4", f.BrokerAddress)
	}
	return []kgo.Opt{kgo.WithContext(ctx), kgo.SeedBrokers(endpoint.Host), kgo.Dialer(dial), kgo.ClientID(id), kgo.SASL(plain.Auth{User: user, Pass: password}.AsMechanism()), kgo.RequestRetries(2), kgo.RetryTimeout(3 * time.Second), kgo.RequestTimeoutOverhead(time.Second), kgo.BrokerMaxReadBytes(1 << 20), kgo.BrokerMaxWriteBytes(1 << 20), kgo.FetchMaxBytes(1 << 20), kgo.MaxBufferedRecords(16), kgo.MaxBufferedBytes(128 << 10), kgo.ProducerBatchMaxBytes(64 << 10), kgo.ProduceRequestTimeout(3 * time.Second), kgo.RecordDeliveryTimeout(8 * time.Second), kgo.RecordRetries(0)}
}

type transactionWireAudit struct {
	mu        sync.Mutex
	role      string
	keys      map[int16]int
	forbidden bool
}

func (a *transactionWireAudit) OnBrokerWrite(_ kgo.BrokerMetadata, key int16, _ int, _ time.Duration, _ time.Duration, _ error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[key]++
	if !transactionWireAllowed(a.role, key) {
		a.forbidden = true
	}
}
func transactionWireAllowed(role string, key int16) bool {
	if role != "oracle" && role != "writer" && role != "output" {
		return false
	}
	allowed := key == 3 || key == 17 || key == 18 || key == 36
	switch role {
	case "oracle":
		return allowed || key == 2 || key == 9 || key == 10 || key == 15
	case "writer":
		return allowed || key == 0 || key == 10 || key == 22 || key == 24 || key == 26
	case "output":
		return allowed || key == 1 || key == 2
	}
	return false
}
func newTransactionDirect(t *testing.T, ctx context.Context, f transactionFixture, trust *tls.Config, role, txID string, extra ...kgo.Opt) *kgo.Client {
	t.Helper()
	if role != "oracle" && role != "writer" && role != "output" {
		t.Fatal("independent transaction client role unsupported")
	}
	user, password := f.OracleUsername, f.OraclePassword
	if role == "writer" {
		user, password = f.WriterUsername, f.WriterPassword
	} else if role == "output" {
		user, password = f.OutputUsername, f.OutputPassword
	}
	audit := &transactionWireAudit{role: role, keys: map[int16]int{}}
	opts := transactionDirectOptions(ctx, f, trust, user, password, "apex-transaction-"+role)
	opts = append(opts, kgo.WithHooks(audit))
	if txID != "" {
		if role != "writer" || !strings.HasPrefix(txID, f.TransactionalPrefix) || len(txID) > 212 {
			t.Fatal("transaction fixture writer ID outside exact approved prefix")
		}
		opts = append(opts, kgo.TransactionalID(txID), kgo.TransactionTimeout(30*time.Second))
	}
	opts = append(opts, extra...)
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal("bounded independent transaction client construction failed")
	}
	if cl.OptValue(kgo.ConsumerGroup) != "" {
		cl.Close()
		t.Fatal("independent transaction client must never join a consumer group")
	}
	t.Cleanup(func() {
		cl.Close()
		audit.mu.Lock()
		defer audit.mu.Unlock()
		if audit.forbidden {
			t.Errorf("independent %s client issued forbidden API", role)
		}
		t.Logf("independent %s direct API counts=%v; physically closed", role, audit.keys)
	})
	return cl
}

type transactionOracle struct {
	cl       *kgo.Client
	settings transactionFixture
}

func newTransactionOracle(t *testing.T, ctx context.Context, f transactionFixture, trust *tls.Config) *transactionOracle {
	t.Helper()
	cl := newTransactionDirect(t, ctx, f, trust, "oracle", "")
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	for _, name := range []string{f.VisibilityTopic, f.InputTopic, f.OutputTopic} {
		name := name
		req.Topics = append(req.Topics, kmsg.MetadataRequestTopic{Topic: &name})
	}
	response, err := cl.Request(ctx, req)
	if err != nil {
		t.Fatal("independent transaction metadata unavailable")
	}
	r, ok := response.(*kmsg.MetadataResponse)
	if !ok || r.ClusterID == nil || *r.ClusterID != f.ClusterID || len(r.Topics) != 3 {
		t.Fatal("independent transaction cluster identity mismatch")
	}
	seen := map[string]bool{}
	for _, topic := range r.Topics {
		if topic.Topic == nil || !f.approvedTopic(*topic.Topic) || seen[*topic.Topic] || topic.ErrorCode != 0 || len(topic.Partitions) != 1 || topic.Partitions[0].Partition != 0 || topic.Partitions[0].ErrorCode != 0 {
			t.Fatal("independent transaction topic inventory mismatch")
		}
		seen[*topic.Topic] = true
	}
	endpoint, _ := url.Parse(f.Endpoint)
	if len(r.Brokers) != 1 || net.JoinHostPort(r.Brokers[0].Host, strconv.Itoa(int(r.Brokers[0].Port))) != endpoint.Host {
		t.Fatal("independent transaction broker inventory mismatch")
	}
	return &transactionOracle{cl: cl, settings: f}
}
func (f transactionFixture) approvedTopic(topic string) bool {
	return topic == f.VisibilityTopic || topic == f.InputTopic || topic == f.OutputTopic
}
func (f transactionFixture) approvedGroup(group string) bool {
	return group == f.Group || group == f.SeekGroup || group == f.FenceGroup
}
func transactionListOffsetsRequest(f transactionFixture, topic string, isolation int8) (*kmsg.ListOffsetsRequest, error) {
	if !f.approvedTopic(topic) || (isolation != 0 && isolation != 1) {
		return nil, errors.New("independent position request outside fixed scope")
	}
	req := kmsg.NewPtrListOffsetsRequest()
	req.IsolationLevel = isolation
	p := kmsg.NewListOffsetsRequestTopicPartition()
	p.Partition = 0
	p.Timestamp = -1
	req.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: topic, Partitions: []kmsg.ListOffsetsRequestTopicPartition{p}}}
	return req, nil
}
func (o *transactionOracle) Next(t *testing.T, ctx context.Context, topic string, isolation int8) int64 {
	t.Helper()
	req, err := transactionListOffsetsRequest(o.settings, topic, isolation)
	if err != nil {
		t.Fatal("independent position request outside fixed scope")
	}
	shards := o.cl.RequestSharded(ctx, req)
	if len(shards) != 1 || shards[0].Err != nil {
		t.Fatal("independent position request failed")
	}
	r, ok := shards[0].Resp.(*kmsg.ListOffsetsResponse)
	if !ok || len(r.Topics) != 1 || r.Topics[0].Topic != topic || len(r.Topics[0].Partitions) != 1 {
		t.Fatal("independent position response incomplete")
	}
	result := r.Topics[0].Partitions[0]
	if result.Partition != 0 || result.ErrorCode != 0 || result.Offset < 0 {
		t.Fatal("independent position response invalid")
	}
	return result.Offset
}
func (o *transactionOracle) Committed(t *testing.T, ctx context.Context, group, topic string) int64 {
	t.Helper()
	if !o.settings.approvedGroup(group) || !o.settings.approvedTopic(topic) || group == o.settings.SeekGroup && topic != o.settings.VisibilityTopic || group != o.settings.SeekGroup && topic != o.settings.InputTopic {
		t.Fatal("independent committed request outside exact scope")
	}
	req := transactionStableOffsetRequest(group, topic)
	value, retries, err := transactionReadStableOffset(ctx, group, topic, func(ctx context.Context) (kmsg.Response, error) { return o.cl.Request(ctx, req) })
	if err != nil {
		t.Fatalf("independent stable committed read failed (class=%s)", ownedSeekOracleFailureClass(err))
	}
	if retries > 0 {
		t.Logf("independent actual stable-offset response retried %d times after Kafka code 88", retries)
	}
	return value
}
func transactionStableOffsetRequest(group, topic string) *kmsg.OffsetFetchRequest {
	req := kmsg.NewPtrOffsetFetchRequest()
	req.RequireStable = true
	req.Group = group
	req.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: []int32{0}}}
	g := kmsg.NewOffsetFetchRequestGroup()
	g.Group = group
	g.Topics = []kmsg.OffsetFetchRequestGroupTopic{{Topic: topic, Partitions: []int32{0}}}
	req.Groups = []kmsg.OffsetFetchRequestGroup{g}
	return req
}
func transactionStableOffsetValue(group, topic string, response kmsg.Response) (int64, error) {
	r, ok := response.(*kmsg.OffsetFetchResponse)
	if !ok || r.Version < 7 || r.ErrorCode != 0 {
		return 0, errors.New("stable committed response invalid")
	}
	var name string
	var partition int32
	var offset int64
	var code int16
	if r.Version < 8 {
		if len(r.Topics) != 1 || len(r.Topics[0].Partitions) != 1 {
			return 0, errors.New("stable committed response incomplete")
		}
		name = r.Topics[0].Topic
		p := r.Topics[0].Partitions[0]
		partition, offset, code = p.Partition, p.Offset, p.ErrorCode
	} else {
		if len(r.Groups) != 1 || r.Groups[0].Group != group || r.Groups[0].ErrorCode != 0 || len(r.Groups[0].Topics) != 1 || len(r.Groups[0].Topics[0].Partitions) != 1 {
			return 0, errors.New("stable committed group response incomplete")
		}
		t := r.Groups[0].Topics[0]
		name = t.Topic
		p := t.Partitions[0]
		partition, offset, code = p.Partition, p.Offset, p.ErrorCode
	}
	if name != topic || partition != 0 || offset < -1 {
		return 0, errors.New("stable committed response escaped exact inventory")
	}
	if code != 0 {
		return 0, kerr.ErrorForCode(code)
	}
	return offset, nil
}
func transactionReadStableOffset(ctx context.Context, group, topic string, request func(context.Context) (kmsg.Response, error)) (value int64, retries int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		response, _, _, requestErr := ownedSeekReadOffsetFetch(ctx, request)
		if requestErr != nil {
			return 0, retries, requestErr
		}
		if err := ctx.Err(); err != nil {
			return 0, retries, err
		}
		value, err = transactionStableOffsetValue(group, topic, response)
		if !errors.Is(err, kerr.UnstableOffsetCommit) {
			return value, retries, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, retries, ctx.Err()
		case <-timer.C:
			retries++
		}
	}
}

func transactionProduce(t *testing.T, ctx context.Context, cl *kgo.Client, topic, marker string) int64 {
	t.Helper()
	record := &kgo.Record{Topic: topic, Partition: 0, Value: []byte(marker)}
	produceCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := cl.ProduceSync(produceCtx, record).FirstErr(); err != nil {
		t.Fatalf("actual fixture write acknowledgement failed (class=%s)", ownedSeekOracleFailureClass(err))
	}
	if record.Topic != topic || record.Partition != 0 || record.Offset < 0 || string(record.Value) != marker {
		t.Fatal("actual fixture acknowledgement identity changed")
	}
	return record.Offset
}
func transactionEnd(t *testing.T, ctx context.Context, cl *kgo.Client, commit kgo.TransactionEndTry) {
	t.Helper()
	endCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := cl.EndTransaction(endCtx, commit); err != nil {
		t.Fatalf("actual fixture transaction end failed (class=%s)", ownedSeekOracleFailureClass(err))
	}
}
func newTransactionExecutor(t *testing.T, ctx context.Context, f transactionFixture, topic, group, id string, consumerTransactions bool) (*Kafka, *franzClient) {
	t.Helper()
	endpoint, _ := url.Parse(f.Endpoint)
	cfg := map[string]any{"kafka_mock": false, "kafka_brokers": []string{endpoint.Host}, "kafka_topic": topic, "kafka_consumer_group": group, "kafka_security_protocol": "SASL_SSL", "kafka_sasl_mechanism": "plain", "kafka_sasl_username": f.Username, "kafka_sasl_password": f.Password, "kafka_tls_ca_cert_file": f.CAFile, "kafka_auto_offset_reset": "earliest", "kafka_isolation_level": "read_committed", "kafka_client_id": "apex-transaction-" + id, "kafka_timeout_ms": 10000}
	if consumerTransactions {
		cfg["kafka_consumer_transactions"] = true
		cfg["kafka_transactional_id"] = f.TransactionalPrefix + id
	}
	instance := &Kafka{name: "kafka"}
	if instance.Init(ctx, cfg) != nil {
		t.Fatal("production transaction executor initialization failed")
	}
	client, ok := instance.client.(*franzClient)
	if !ok || instance.cfg.Mock {
		t.Fatal("transaction qualification requires the production real adapter")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if instance.Teardown(cleanupCtx) != nil {
			t.Error("production transaction executor teardown failed")
		}
	})
	return instance, client
}
func transactionExecute(t *testing.T, ctx context.Context, k *Kafka, action *protocol.Action) *protocol.Result {
	t.Helper()
	result, err := k.Execute(ctx, action)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("production transaction action %s failed", action.Type)
	}
	return result
}
