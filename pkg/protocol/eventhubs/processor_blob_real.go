//go:build eventhubs_real

package eventhubs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/google/uuid"
)

var errCheckpointFence = errors.New("Event Hubs checkpoint ownership fence changed")

type partitionCheckpointRecord struct {
	Name, Owner, Offset, ETag, Generation string
	Sequence                              *int64
	Modified                              time.Time
}
type checkpointRecordIO interface {
	List(context.Context, string) ([]partitionCheckpointRecord, error)
	Read(context.Context, string) (partitionCheckpointRecord, error)
	Write(context.Context, partitionCheckpointRecord, string) (partitionCheckpointRecord, error)
}

// Ownership and checkpoint share one Blob metadata record. Conditional writes
// fence the checkpoint against ownership takeover atomically; independent blobs
// cannot provide this boundary.
type fencedCheckpointStore struct {
	mu                           sync.Mutex
	records                      checkpointRecordIO
	namespace, hub, group, owner string
	partitions                   map[string]bool
	progress                     *processorProgress
}

func (s *fencedCheckpointStore) prefix() string {
	return "apexload-processor-v1/" + url.PathEscape(s.namespace) + "/" + url.PathEscape(s.hub) + "/" + url.PathEscape(s.group) + "/"
}
func (s *fencedCheckpointStore) scope(namespace, hub, group, partition string) error {
	if namespace != s.namespace || hub != s.hub || group != s.group || partition != "" && !s.partitions[partition] {
		return fmt.Errorf("checkpoint scope does not match the configured processor")
	}
	return nil
}
func (s *fencedCheckpointStore) ListOwnership(ctx context.Context, namespace, hub, group string, _ *eh.ListOwnershipOptions) ([]eh.Ownership, error) {
	if err := s.scope(namespace, hub, group, ""); err != nil {
		return nil, err
	}
	values, err := s.records.List(ctx, s.prefix())
	if err != nil {
		s.progress.unverified("")
		return nil, err
	}
	out := make([]eh.Ownership, 0, len(values))
	seen := map[string]bool{}
	for _, r := range values {
		partition, err := s.partition(r.Name)
		if err != nil {
			return nil, err
		}
		seen[partition] = true
		if r.Owner == s.owner {
			s.progress.ownership(partition, r.Generation, "owned", r.Sequence, r.Offset)
		} else {
			s.progress.observeForeignOwner(partition, r.Generation)
		}
		etag := azcore.ETag(r.ETag)
		out = append(out, eh.Ownership{FullyQualifiedNamespace: namespace, EventHubName: hub, ConsumerGroup: group, PartitionID: partition, OwnerID: r.Owner, LastModifiedTime: r.Modified, ETag: &etag})
	}
	for id := range s.partitions {
		if !seen[id] {
			s.progress.observeForeignOwner(id, "")
		}
	}
	return out, nil
}
func (s *fencedCheckpointStore) partition(name string) (string, error) {
	if !strings.HasPrefix(name, s.prefix()) {
		return "", fmt.Errorf("checkpoint record outside processor scope")
	}
	partition, err := url.PathUnescape(strings.TrimPrefix(name, s.prefix()))
	if err != nil || !s.partitions[partition] {
		return "", fmt.Errorf("checkpoint record has an unknown partition")
	}
	return partition, nil
}
func (s *fencedCheckpointStore) ClaimOwnership(ctx context.Context, claims []eh.Ownership, _ *eh.ClaimOwnershipOptions) ([]eh.Ownership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(claims) > 32 {
		return nil, fmt.Errorf("checkpoint ownership request exceeds partition limit")
	}
	var out []eh.Ownership
	for _, claim := range claims {
		if err := s.scope(claim.FullyQualifiedNamespace, claim.EventHubName, claim.ConsumerGroup, claim.PartitionID); err != nil {
			return nil, err
		}
		if !s.partitions[claim.PartitionID] {
			return nil, fmt.Errorf("checkpoint claim requires a known partition")
		}
		if claim.OwnerID != "" && claim.OwnerID != s.owner {
			return nil, fmt.Errorf("checkpoint claim has a different processor identity")
		}
		name := s.prefix() + url.PathEscape(claim.PartitionID)
		current, err := s.records.Read(ctx, name)
		if err != nil {
			s.progress.unverified(claim.PartitionID)
			return nil, err
		}
		if current.Owner != s.owner {
			s.progress.observeForeignOwner(claim.PartitionID, current.Generation)
		}
		expected := ""
		if claim.ETag != nil {
			expected = string(*claim.ETag)
		}
		// A successful local checkpoint can renew our own ETag between listing and
		// claiming. It cannot justify taking over another owner's changed generation.
		if current.ETag != expected && !(current.Owner == s.owner && (claim.OwnerID == s.owner || claim.OwnerID == "")) {
			continue
		}
		if claim.OwnerID == "" && current.Owner != s.owner {
			continue
		}
		if current.Owner != claim.OwnerID || current.Generation == "" {
			current.Generation = uuid.NewString()
		}
		current.Name = name
		current.Owner = claim.OwnerID
		updated, err := s.records.Write(ctx, current, current.ETag)
		if errors.Is(err, errCheckpointFence) {
			s.progress.unverified(claim.PartitionID)
			continue
		}
		if err != nil {
			s.progress.unverified(claim.PartitionID)
			return nil, err
		}
		if updated.Owner == "" {
			s.progress.releasePartition(claim.PartitionID)
		} else {
			s.progress.ownership(claim.PartitionID, updated.Generation, "owned", updated.Sequence, updated.Offset)
		}
		etag := azcore.ETag(updated.ETag)
		claim.ETag = &etag
		claim.LastModifiedTime = updated.Modified
		out = append(out, claim)
	}
	return out, nil
}
func (s *fencedCheckpointStore) ListCheckpoints(ctx context.Context, namespace, hub, group string, _ *eh.ListCheckpointsOptions) ([]eh.Checkpoint, error) {
	if err := s.scope(namespace, hub, group, ""); err != nil {
		return nil, err
	}
	values, err := s.records.List(ctx, s.prefix())
	if err != nil {
		return nil, err
	}
	var out []eh.Checkpoint
	for _, r := range values {
		partition, err := s.partition(r.Name)
		if err != nil {
			return nil, err
		}
		if r.Sequence != nil {
			seq := *r.Sequence
			offset := r.Offset
			out = append(out, eh.Checkpoint{FullyQualifiedNamespace: namespace, EventHubName: hub, ConsumerGroup: group, PartitionID: partition, SequenceNumber: &seq, Offset: &offset})
		}
	}
	return out, nil
}
func (s *fencedCheckpointStore) SetCheckpoint(ctx context.Context, cp eh.Checkpoint, _ *eh.SetCheckpointOptions) (result error) {
	started := time.Now()
	fence, _ := ctx.Value(checkpointFenceContextKey{}).(string)
	wrote := false
	if s.progress != nil && s.partitions[cp.PartitionID] && fence != "" {
		retry, _ := ctx.Value(checkpointRetryContextKey{}).(bool)
		s.progress.checkpointBegin(cp.PartitionID, fence, retry)
		defer func() {
			outcome := "failed"
			switch {
			case result == nil:
				outcome = "committed"
			case errors.Is(result, errCheckpointFence):
				outcome = "fenced"
			case ctx.Err() != nil && !wrote:
				outcome = "cancelled_before_write"
			case wrote:
				outcome = "unconfirmed"
			}
			offset := ""
			if cp.Offset != nil {
				offset = *cp.Offset
			}
			s.progress.checkpointEnd(cp.PartitionID, fence, outcome, cp.SequenceNumber, offset, time.Since(started))
		}()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scope(cp.FullyQualifiedNamespace, cp.EventHubName, cp.ConsumerGroup, cp.PartitionID); err != nil {
		return err
	}
	if !s.partitions[cp.PartitionID] {
		return fmt.Errorf("checkpoint requires a known partition")
	}
	if cp.SequenceNumber == nil || *cp.SequenceNumber < 0 || cp.Offset == nil || !validProviderOffset(*cp.Offset) {
		return fmt.Errorf("checkpoint observation is unavailable or exceeds limits")
	}
	current, err := s.records.Read(ctx, s.prefix()+url.PathEscape(cp.PartitionID))
	if err != nil {
		s.progress.unverified(cp.PartitionID)
		return err
	}
	if current.Owner != s.owner || current.ETag == "" || fence == "" || fence != current.Generation {
		return errCheckpointFence
	}
	if current.Sequence != nil && *cp.SequenceNumber == *current.Sequence && *cp.Offset != current.Offset {
		return fmt.Errorf("checkpoint offset differs for the same sequence")
	}
	if current.Sequence != nil && *cp.SequenceNumber < *current.Sequence {
		return fmt.Errorf("checkpoint sequence cannot move backward")
	}
	seq := *cp.SequenceNumber
	current.Sequence = &seq
	current.Offset = *cp.Offset
	if err := ctx.Err(); err != nil {
		return err
	}
	wrote = true
	_, err = s.records.Write(ctx, current, current.ETag)
	return err
}

type blobCheckpointRecords struct{ client *container.Client }

func (b blobCheckpointRecords) List(ctx context.Context, prefix string) ([]partitionCheckpointRecord, error) {
	max := int32(33)
	pager := b.client.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix, MaxResults: &max, Include: container.ListBlobsInclude{Metadata: true}})
	var out []partitionCheckpointRecord
	for pages := 0; pager.More(); pages++ {
		if pages >= 4 {
			return nil, fmt.Errorf("checkpoint listing page limit exceeded")
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		if page.Segment == nil {
			return nil, fmt.Errorf("checkpoint listing is incomplete")
		}
		for _, item := range page.Segment.BlobItems {
			if len(out) >= 32 || item == nil || item.Name == nil || item.Properties == nil || item.Properties.ETag == nil || item.Properties.LastModified == nil {
				return nil, fmt.Errorf("checkpoint listing exceeds limits or lacks provenance")
			}
			r, err := decodeCheckpointMetadata(*item.Name, item.Metadata, string(*item.Properties.ETag), *item.Properties.LastModified)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
	}
	return out, nil
}
func (b blobCheckpointRecords) Read(ctx context.Context, name string) (partitionCheckpointRecord, error) {
	props, err := b.client.NewBlobClient(name).GetProperties(ctx, nil)
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return partitionCheckpointRecord{Name: name}, nil
	}
	if err != nil {
		return partitionCheckpointRecord{}, err
	}
	if props.ETag == nil || props.LastModified == nil {
		return partitionCheckpointRecord{}, fmt.Errorf("checkpoint metadata is incomplete")
	}
	return decodeCheckpointMetadata(name, props.Metadata, string(*props.ETag), *props.LastModified)
}
func (b blobCheckpointRecords) Write(ctx context.Context, r partitionCheckpointRecord, expected string) (partitionCheckpointRecord, error) {
	owner := r.Owner
	if owner == "" {
		owner = "_"
	}
	generation := r.Generation
	metadata := map[string]*string{"apex_owner": &owner, "apex_generation": &generation}
	if r.Sequence != nil {
		seq := strconv.FormatInt(*r.Sequence, 10)
		offset := r.Offset
		metadata["apex_sequence"] = &seq
		metadata["apex_offset"] = &offset
	}
	var etag *azcore.ETag
	var modified *time.Time
	var err error
	if expected == "" {
		star := azcore.ETag("*")
		resp, e := b.client.NewBlockBlobClient(r.Name).UploadBuffer(ctx, []byte{}, &blockblob.UploadBufferOptions{Metadata: metadata, AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: &star}}})
		err = e
		etag = resp.ETag
		modified = resp.LastModified
	} else {
		match := azcore.ETag(expected)
		resp, e := b.client.NewBlobClient(r.Name).SetMetadata(ctx, metadata, &blob.SetMetadataOptions{AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: &match}}})
		err = e
		etag = resp.ETag
		modified = resp.LastModified
	}
	if bloberror.HasCode(err, bloberror.ConditionNotMet, bloberror.BlobAlreadyExists, bloberror.BlobNotFound) {
		return partitionCheckpointRecord{}, errCheckpointFence
	}
	if err != nil {
		return partitionCheckpointRecord{}, err
	}
	if etag == nil || modified == nil {
		return partitionCheckpointRecord{}, fmt.Errorf("checkpoint write lacks durable provenance")
	}
	r.ETag = string(*etag)
	r.Modified = *modified
	return r, nil
}
func decodeCheckpointMetadata(name string, m map[string]*string, etag string, modified time.Time) (partitionCheckpointRecord, error) {
	normalized := map[string]*string{}
	for key, value := range m {
		canonical := strings.ToLower(key)
		if canonical != "apex_owner" && canonical != "apex_generation" && canonical != "apex_sequence" && canonical != "apex_offset" {
			return partitionCheckpointRecord{}, fmt.Errorf("unknown checkpoint metadata key")
		}
		if _, exists := normalized[canonical]; exists {
			return partitionCheckpointRecord{}, fmt.Errorf("duplicate checkpoint metadata key")
		}
		normalized[canonical] = value
	}
	m = normalized
	r := partitionCheckpointRecord{Name: name, ETag: etag, Modified: modified}
	if len(name) > 2048 || len(m) > 4 || m["apex_owner"] == nil || len(*m["apex_owner"]) > 256 {
		return r, fmt.Errorf("invalid checkpoint metadata")
	}
	r.Owner = *m["apex_owner"]
	if r.Owner == "_" {
		r.Owner = ""
	}
	if m["apex_generation"] == nil || len(*m["apex_generation"]) != 36 {
		return r, fmt.Errorf("invalid checkpoint generation")
	}
	r.Generation = *m["apex_generation"]
	if m["apex_sequence"] == nil && m["apex_offset"] != nil {
		return r, fmt.Errorf("checkpoint offset has no sequence")
	}
	if seq := m["apex_sequence"]; seq != nil {
		value, err := strconv.ParseInt(*seq, 10, 64)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != *seq || m["apex_offset"] == nil || !validProviderOffset(*m["apex_offset"]) {
			return r, fmt.Errorf("invalid checkpoint sequence")
		}
		r.Sequence = &value
		r.Offset = *m["apex_offset"]
	}
	return r, nil
}

type checkpointHTTP struct {
	client     *http.Client
	host, path string
}

func (t checkpointHTTP) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != t.host || req.URL.Path != t.path && !strings.HasPrefix(req.URL.Path, t.path+"/") {
		return nil, fmt.Errorf("checkpoint request destination is outside approved storage")
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("checkpoint response limit exceeded")
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

var checkpointSharedHTTP = func() *http.Client {
	transport := &http.Transport{}
	if configured, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = configured.Clone()
	}
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}()

func newCheckpointBlobClient(containerURL string, cred azcore.TokenCredential) (*container.Client, error) {
	u, err := url.Parse(containerURL)
	if err != nil {
		return nil, err
	}
	return container.NewClient(containerURL, cred, &container.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: checkpointHTTP{client: checkpointSharedHTTP, host: u.Host, path: u.Path}, Retry: policy.RetryOptions{MaxRetries: 2, TryTimeout: 5 * time.Second}}})
}

type checkpointFenceContextKey struct{}
type checkpointRetryContextKey struct{}

func (s *fencedCheckpointStore) ownershipFence(ctx context.Context, partition string) (string, error) {
	if !s.partitions[partition] {
		return "", errCheckpointFence
	}
	current, err := s.records.Read(ctx, s.prefix()+url.PathEscape(partition))
	if err != nil {
		s.progress.unverified(partition)
		return "", err
	}
	if current.Owner != s.owner || current.Generation == "" {
		s.progress.observeForeignOwner(partition, current.Generation)
		return "", errCheckpointFence
	}
	s.progress.ownership(partition, current.Generation, "owned", current.Sequence, current.Offset)
	return current.Generation, nil
}
