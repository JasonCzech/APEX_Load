package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"
)

const pitKeepAlive = "2m"

// Only PIT searches install this observer. Dispatch can renew upstream
// ownership even when the page is subsequently rejected or its body is lost.
type pitSearchAttemptKey struct{}
type pitSearchAttempt struct {
	dispatched bool
	responseID string
}

func (attempt *pitSearchAttempt) observeResponse(data []byte) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return
	}
	id, found := "", false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			break
		}
		if key == "pit_id" {
			// A repeated identity is ambiguous in an incomplete response.
			if found {
				return
			}
			found = true
			if decoder.Decode(&id) != nil || !validOwnedHandle(id) {
				return
			}
		} else if skipPITResponseValue(decoder) != nil {
			break
		}
	}
	if id != "" {
		attempt.responseID = id
	}
}

// Walk only tokens while skipping other top-level values. Never build or
// retain nested source documents to recover a private ownership identifier.
func skipPITResponseValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid response value")
	}
	for depth := 1; depth > 0; {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
				if depth > 128 {
					return errors.New("response nesting exceeds ownership inspection bound")
				}
			} else {
				depth--
			}
		}
	}
	return nil
}

// searchPIT retains the latest bounded owner independently of page acceptance.
// The post-request horizon conservatively covers renewal during any dispatched
// attempt; a request stopped before dispatch cannot fabricate a renewed TTL.
func (c *Client) searchPIT(ctx context.Context, cursor *Continuation, body any, response *searchResponse) error {
	attempt := &pitSearchAttempt{}
	*response = searchResponse{}
	err := c.request(context.WithValue(ctx, pitSearchAttemptKey{}, attempt), "POST", "/_search?allow_partial_search_results=false", body, response)
	if attempt.dispatched {
		until := time.Now().UTC().Add(2 * time.Minute)
		if until.After(cursor.KeepAliveUntil) {
			cursor.KeepAliveUntil = until
		}
	}
	identityErr := updatePIT(cursor, response.PITID)
	if attempt.responseID != "" {
		cursor.PITID = attempt.responseID
	}
	if err != nil {
		return err
	}
	return identityErr
}

func (c *Client) restartPIT(ctx context.Context, indices []string, cursor *Continuation, result *Result) error {
	// A 404 may originate from an intermediary rather than an expired PIT.
	// Preserve the previous (possibly rotated) owner before replacing it.
	c.retainFailedCleanup(ctx, cursor, result)
	cursor.PITID = ""
	cursor.KeepAliveUntil = time.Time{}
	cursor.SearchAfter, cursor.CompositeAfter = nil, nil
	if !c.cleanupCapacity(result, 1) {
		return ErrBudget
	}
	id, err := c.openPIT(ctx, indices)
	cursor.PITID = id
	if id != "" {
		cursor.KeepAliveUntil = time.Now().UTC().Add(2 * time.Minute)
	}
	return err
}

func (c *Client) openPIT(ctx context.Context, indices []string) (string, error) {
	var r struct {
		ID     string         `json:"id"`
		PITID  string         `json:"pit_id"`
		Shards responseShards `json:"_shards"`
	}
	path := "/" + indexPath(indices) + "/_pit?keep_alive=" + pitKeepAlive
	// Elasticsearch 8.14 does not accept this creation parameter. Strict
	// search responses still reject incomplete shards on every page.
	if versionAtLeast(c.capabilitiesSnapshot().Version, 8, 18) {
		path += "&allow_partial_search_results=false"
	}
	if c.cfg.Product == ProductOpenSearch {
		path = "/" + indexPath(indices) + "/_search/point_in_time?keep_alive=" + pitKeepAlive + "&allow_partial_pit_creation=false"
	}
	requestErr := c.request(ctx, "POST", path, nil, &r)
	id := r.ID
	if c.cfg.Product == ProductOpenSearch {
		id = r.PITID
	}
	if requestErr != nil {
		// A type error can leave a bounded handle populated in an otherwise
		// rejected response. Preserve that ownership for the caller cleanup.
		if validOwnedHandle(id) {
			return id, requestErr
		}
		return "", requestErr
	}
	if r.Shards.partial() || !validOwnedHandle(id) {
		if validOwnedHandle(id) {
			cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			closeErr := c.closePIT(cleanupCtx, id)
			cancel()
			if closeErr != nil && !cleanupAlreadyGone(closeErr) {
				return id, errors.New("PIT creation returned incomplete shards or invalid handle")
			}
		}
		return "", errors.New("PIT creation returned incomplete shards or invalid handle")
	}
	return id, nil
}
func (c *Client) closePIT(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	path, body := "/_pit", map[string]any{"id": id}
	if c.cfg.Product == ProductOpenSearch {
		path = "/_search/point_in_time"
		body = map[string]any{"pit_id": []string{id}}
	}
	var r struct {
		Succeeded bool `json:"succeeded"`
		Pits      []struct {
			Successful bool `json:"successful"`
		} `json:"pits"`
	}
	if e := c.requestAt(ctx, "", "DELETE", path, body, &r, true); e != nil {
		return e
	}
	if c.cfg.Product == ProductOpenSearch {
		if len(r.Pits) != 1 || !r.Pits[0].Successful {
			return errors.New("PIT cleanup did not succeed")
		}
	} else if !r.Succeeded {
		return errors.New("PIT cleanup did not succeed")
	}
	return nil
}

// CloseContinuation releases only the exact context owned by this job. It is
// safe to call after cancellation using a separately bounded cleanup context.
func (c *Client) CloseContinuation(ctx context.Context, cursor *Continuation) (closeErr error) {
	if cursor == nil {
		return nil
	}
	if e := cursor.Validate(); e != nil {
		return e
	}
	defer func() {
		if closeErr != nil && !cleanupAlreadyGone(closeErr) {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		retained := c.pendingCleanup[:0]
		for _, owned := range c.pendingCleanup {
			if owned.PITID != cursor.PITID || owned.AsyncID != cursor.AsyncID {
				retained = append(retained, owned)
			}
		}
		c.pendingCleanup = retained
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if cursor.PITID != "" {
		return c.closePIT(ctx, cursor.PITID)
	}
	if cursor.AsyncID != "" {
		return c.requestAt(ctx, "", "DELETE", "/_query/async/"+escapeHandle(cursor.AsyncID), nil, nil, true)
	}
	return nil
}
func (c *Client) cleanup(cursor *Continuation) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.CloseContinuation(ctx, cursor)
}

// retainFailedCleanup keeps ownership until an executor durably queues cleanup.
// A failed close never changes the independently established query coverage.
func (c *Client) retainFailedCleanup(parent context.Context, cursor *Continuation, result *Result) {
	if cursor == nil || cursor.PITID == "" && cursor.AsyncID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	err := c.CloseContinuation(cleanupCtx, cursor)
	cancel()
	if err == nil || cleanupAlreadyGone(err) || !cursor.KeepAliveUntil.IsZero() && !cursor.KeepAliveUntil.After(time.Now().UTC()) {
		return
	}
	copy := *cursor
	// Cleanup only needs ownership and the fixed window, not private pagination
	// data. Omitting those vectors keeps the durable cleanup batch bounded.
	copy.ResolvedIndices, copy.SearchAfter, copy.CompositeAfter = nil, nil, nil
	if result != nil {
		for _, owned := range result.CleanupContinuations {
			if reflect.DeepEqual(owned, &copy) {
				return
			}
		}
		result.CleanupContinuations = append(result.CleanupContinuations, &copy)
	} else {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, owned := range c.pendingCleanup {
			if reflect.DeepEqual(owned, &copy) {
				return
			}
		}
		c.pendingCleanup = append(c.pendingCleanup, &copy)
	}
}
func cleanupAlreadyGone(err error) bool {
	var response *HTTPError
	return errors.As(err, &response) && response.Status == http.StatusNotFound
}
func validOwnedHandle(id string) bool {
	return id != "" && len(id) <= 16384 && !strings.ContainsAny(id, "\r\n\x00")
}

const maxPrivateCleanupBytes = 256 << 10

// JSON escapes can expand each byte of a bounded upstream handle to six bytes.
// Reserve framing, query identity and timestamps before opening another handle.
const maxPrivateCleanupHandleReserve = 6*16384 + 4096

func (c *Client) cleanupCapacity(result *Result, additional int) bool {
	owned := c.PendingCleanupContinuations()
	if result != nil {
		owned = append(owned, result.CleanupContinuations...)
	}
	if additional < 0 || len(owned)+additional > 32 {
		return false
	}
	body, err := json.Marshal(owned)
	return err == nil && len(body)+additional*maxPrivateCleanupHandleReserve <= maxPrivateCleanupBytes
}
