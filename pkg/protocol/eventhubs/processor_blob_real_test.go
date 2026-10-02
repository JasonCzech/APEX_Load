//go:build eventhubs_real

package eventhubs

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCheckpointBlobSDKConditionalMetadataRoundTrip(t *testing.T) {
	metadata := map[string]string{}
	etag := ""
	revision := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if etag == "" && r.Header.Get("If-None-Match") != "*" || etag != "" && r.Header.Get("If-Match") != etag {
				w.Header().Set("x-ms-error-code", "ConditionNotMet")
				w.WriteHeader(412)
				return
			}
			metadata = map[string]string{}
			for key, values := range r.Header {
				if strings.HasPrefix(strings.ToLower(key), "x-ms-meta-") {
					metadata[key[len("x-ms-meta-"):]] = values[0]
				}
			}
			revision++
			etag = fmt.Sprintf("\"%d\"", revision)
		}
		if r.Method == http.MethodHead && etag == "" {
			w.Header().Set("x-ms-error-code", "BlobNotFound")
			w.WriteHeader(404)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		for key, value := range metadata {
			w.Header().Set("x-ms-meta-"+key, value)
		}
		if r.Method == http.MethodPut && r.URL.Query().Get("comp") != "metadata" {
			w.WriteHeader(201)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/checkpoints"
	u, _ := url.Parse(endpoint)
	client, err := container.NewClientWithNoCredential(endpoint, &container.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: checkpointHTTP{client: server.Client(), host: u.Host, path: u.Path}, Retry: policy.RetryOptions{MaxRetries: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	io := blobCheckpointRecords{client: client}
	record := partitionCheckpointRecord{Name: "partitions/0", Owner: "worker", Generation: "82d72e25-5524-4110-91cc-11c17238c1b1"}
	created, err := io.Write(context.Background(), record, "")
	if err != nil || created.ETag == "" {
		t.Fatal("create", err)
	}
	seq := int64(4)
	created.Sequence = &seq
	created.Offset = "4"
	updated, err := io.Write(context.Background(), created, created.ETag)
	if err != nil {
		t.Fatal("update", err)
	}
	read, err := io.Read(context.Background(), record.Name)
	if err != nil || read.ETag != updated.ETag || read.Owner != "worker" || read.Sequence == nil || *read.Sequence != 4 {
		t.Fatalf("metadata round trip %+v %v", read, err)
	}
	if _, err := io.Write(context.Background(), created, created.ETag); err != errCheckpointFence {
		t.Fatalf("stale ETag accepted %v", err)
	}
}
func TestCheckpointTransportBoundsDestinationAndResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (2<<20)+1))) }))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	transport := checkpointHTTP{client: server.Client(), host: u.Host, path: "/checkpoints"}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/other", nil)
	if _, err := transport.Do(request); err == nil {
		t.Fatal("unapproved container accessed")
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/checkpoints", nil)
	if _, err := transport.Do(request); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestCheckpointMetadataRejectsAmbiguousRecords(t *testing.T) {
	owner, generation, offset := "worker", "82d72e25-5524-4110-91cc-11c17238c1b1", "4"
	for _, metadata := range []map[string]*string{
		{"apex_owner": &owner, "APEX_OWNER": &owner, "apex_generation": &generation},
		{"apex_owner": &owner, "apex_generation": &generation, "apex_offset": &offset},
		{"apex_owner": &owner, "apex_generation": &generation, "unknown": &offset},
	} {
		if _, err := decodeCheckpointMetadata("partition", metadata, "etag", time.Now()); err == nil {
			t.Fatal("ambiguous metadata accepted")
		}
	}
}
