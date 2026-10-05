package collectoragent

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"io"
	"os"
	"time"
)

type managementSpoolRejection struct {
	PollID     string `json:"poll_id"`
	SourceID   string `json:"source_id"`
	Generation string `json:"generation"`
	Reason     string `json:"reason"`
	Digest     string `json:"digest"`
}
type managementSpool struct {
	Version  int                        `json:"version"`
	Pending  map[string]*RunSampleBatch `json:"pending"`
	Rejected []managementSpoolRejection `json:"rejected"`
}

func (s *managementScheduler) spoolPath() string {
	if s.agent.cfg.SpoolDirectory == "" {
		return ""
	}
	return s.agent.spoolPath() + ".management"
}
func (s *managementScheduler) loadSpool() error {
	path := s.spoolPath()
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCollectorSpoolBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxCollectorSpoolBytes {
		return errors.New("collector management spool exceeds limit")
	}
	var document managementSpool
	if err = strictManagementJSON(data, &document); err != nil || document.Version != 3 || document.Pending == nil || len(document.Pending) > 32 || len(document.Rejected) > 32 {
		return errors.New("invalid management spool; refusing to discard evidence")
	}
	ids := []string{}
	for id, batch := range document.Pending {
		if !managementLabel(id, 256) || batch == nil || batch.EvidenceVersion != ManagementEvidenceVersion || batch.ManagementGrant == nil || batch.MessagingManagement == nil || batch.Sequence < 0 || len(batch.Points) != 0 {
			return errors.New("invalid management spool batch")
		}
		snapshot := batch.MessagingManagement
		if err = snapshot.Validate(); err != nil {
			return err
		}
		grant, err := VerifyManagementPoll(*batch.ManagementGrant, s.agent.cfg.ControllerPublicKey, s.agent.cfg.CollectorID, snapshot.CollectorInstanceID, time.Now().UTC(), true)
		if err != nil {
			return err
		}
		if grant.AcceptedCoreDigest != "" && !managementAcceptedMatches(batch, grant) {
			return errors.New("management spool accepted core mismatch")
		}
		if id != grant.PollID || !snapshotMatchesGrant(snapshot, grant) {
			return errors.New("management spool grant mismatch")
		}
		leading := false
		for _, member := range grant.Cohort {
			ids = append(ids, member.JobID)
			if member.JobID == grant.LeaderJobID {
				leading = batch.Sequence == member.NextSequence
			}
		}
		if !leading {
			return errors.New("management spool leader sequence mismatch")
		}
		body, err := json.Marshal(batch)
		if err != nil || len(body) > 1<<20 {
			return errors.New("management spool upload exceeds bounds")
		}
	}
	for _, r := range document.Rejected {
		if !managementLabel(r.PollID, 256) || !managementLabel(r.SourceID, 256) || !managementHash(r.Digest) || r.Reason != "fenced" {
			return errors.New("invalid management spool audit")
		}
		if _, err := managementDecimal(r.Generation, true); err != nil {
			return err
		}
	}
	if err = s.agent.spoolBudget.restore("management", data, ids); err != nil {
		return err
	}
	s.pending = document.Pending
	s.rejected = document.Rejected
	return nil
}
func (s *managementScheduler) saveSpoolLocked() error {
	document := managementSpool{Version: 3, Pending: s.pending, Rejected: s.rejected}
	if document.Rejected == nil {
		document.Rejected = []managementSpoolRejection{}
	}
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	ids := []string{}
	absorb := []string{}
	for _, batch := range s.pending {
		if batch == nil || batch.ManagementGrant == nil {
			return errors.New("invalid pending management evidence")
		}
		var grant ManagementPollGrant
		if err := json.Unmarshal([]byte(batch.ManagementGrant.Payload), &grant); err != nil {
			return err
		}
		absorb = append(absorb, "management-pending:"+grant.PollID)
		for _, member := range grant.Cohort {
			ids = append(ids, member.JobID)
		}
	}
	return s.agent.spoolBudget.persist("management", s.spoolPath(), data, ids, absorb...)
}
func snapshotMatchesGrant(s *analysis.MessagingManagementSnapshot, g ManagementPollGrant) bool {
	return s != nil && s.SourceID == g.SourceID && s.SourceRevision == g.SourceRevision && s.CollectorID == g.CollectorID && s.CollectorInstanceID == g.CollectorInstanceID && s.ScopeHash == g.ScopeHash && s.IdentityHash == g.IdentityHash && s.Generation == g.Generation && s.PollID == g.PollID && s.PollSequence == g.PollSequence && s.Kind == managementKind(g.Pack)
}
