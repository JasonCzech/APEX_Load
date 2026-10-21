package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

type replayArtifactPublicationStore interface {
	BeginReplayArtifactReceipt(context.Context, storage.ReplayArtifactSpec, string, time.Duration, storage.ReplayArtifactLimits) error
	JoinReplayArtifactWriter(context.Context, string, string, string, string) (bool, error)
	PublishReplayArtifactReceipt(context.Context, string, string, string, string) (bool, error)
}

// A publisher is bound at admission to one session/project/owner, never rebuilt
// from a caller-controlled snapshot. Backend authority is fixed for its lifetime.
type replayArtifactPublisher struct {
	store                   replayArtifactPublicationStore
	backend                 *replayArtifactBackend
	session, project, owner string
	ttl                     time.Duration
	limits                  storage.ReplayArtifactLimits
}

func (p *replayArtifactPublisher) Publish(ctx context.Context, session *recordbridge.BrowserReplaySession, har io.ReadSeeker, size int64) error {
	if p == nil || p.store == nil || p.backend == nil || session == nil || har == nil ||
		session.ID != p.session || session.ProjectID != p.project || p.owner == "" ||
		!recordbridge.ReplaySessionStatusTerminal(session.Status) || session.CompletedAt == nil || session.FinalReport == nil || session.FinalReport.ReplaySessionID != p.session || size <= 0 || size > 256<<20 {
		return errors.New("invalid terminal replay publication")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Start from a known offset; a reader left at EOF cannot stand in for the HAR.
	if _, err := har.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(&archiveContextReader{ctx: ctx, r: har}, size+1))
	if err != nil {
		return err
	}
	if n != size {
		return errors.New("replay HAR length changed")
	}
	if _, err = har.Seek(0, io.SeekStart); err != nil {
		return err
	}
	report, err := json.Marshal(struct {
		Session *recordbridge.BrowserReplaySession `json:"session"`
	}{session})
	if err != nil {
		return err
	}
	if len(report) == 0 || len(report) > 16<<20 {
		return errors.New("replay report exceeds retention limit")
	}
	var entropy [32]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return err
	}
	spec := storage.ReplayArtifactSpec{Version: 1, SessionID: p.session, ProjectID: p.project, Generation: hex.EncodeToString(entropy[:]), SourceSHA256: p.backend.sourceSHA256(), HARSHA256: hex.EncodeToString(hash.Sum(nil)), HARBytes: size, ReportSHA256: sha256BytesHex(report), ReportBytes: int64(len(report))}
	if err = p.store.BeginReplayArtifactReceipt(ctx, spec, p.owner, p.ttl, p.limits); err != nil {
		return err
	}
	if err = p.backend.Stage(ctx, spec, har, bytes.NewReader(report)); err != nil {
		return err
	}
	// Stage's successful verified close supplies physical writer completion, even
	// if cancellation/deletion now prevents publication. Record that proof with an
	// independent bounded context so the exact generation can become cleanable.
	joinCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	joined, err := p.store.JoinReplayArtifactWriter(joinCtx, p.session, p.project, spec.Generation, p.owner)
	cancel()
	if err != nil {
		return err
	}
	if !joined {
		return storage.ErrReplayArtifactFence
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	published, err := p.store.PublishReplayArtifactReceipt(ctx, p.session, p.project, spec.Generation, p.owner)
	if err != nil {
		return err
	}
	if !published {
		return storage.ErrReplayArtifactFence
	}
	return nil
}
