package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func applyElasticConnectionExtensions(c *storage.ELKConnection, old storage.ELKConnection, req ELKConnectionRequest) {
	if req.Config.Endpoint == "" && old.Config.Endpoint == "" && req.Product == "" {
		return
	}
	next := req.Config
	if next.Endpoint == "" {
		next = old.Config
		next.Endpoint = c.Endpoint
		next.AuthType = c.AuthType
	}
	if req.Product != "" {
		next.Product = req.Product
	}
	if next.Product == "" {
		next.Product = elastic.ProductElasticsearch
	}
	if next.Endpoint == old.Config.Endpoint {
		if next.CAPEM == "" {
			next.CAPEM = old.Config.CAPEM
		}
		if next.ApprovedCIDRs == nil {
			next.ApprovedCIDRs = append([]string(nil), old.Config.ApprovedCIDRs...)
		}
	}
	if next.KibanaEndpoint == old.Config.KibanaEndpoint {
		if next.KibanaCAPEM == "" {
			next.KibanaCAPEM = old.Config.KibanaCAPEM
		}
		if next.KibanaApprovedCIDRs == nil {
			next.KibanaApprovedCIDRs = old.Config.KibanaApprovedCIDRs
		}
	}
	if next.LogstashEndpoint == old.Config.LogstashEndpoint {
		if next.LogstashCAPEM == "" {
			next.LogstashCAPEM = old.Config.LogstashCAPEM
		}
		if next.LogstashApprovedCIDRs == nil {
			next.LogstashApprovedCIDRs = old.Config.LogstashApprovedCIDRs
		}
	}
	mode := req.CollectionMode
	if mode == "" {
		mode = old.CollectionMode
	}
	if mode == "" {
		mode = "direct"
	}
	collector := req.CollectorAgentID
	if collector == "" && req.CollectionMode == "" {
		collector = old.CollectorAgentID
	}
	if mode == "direct" {
		collector = ""
	}
	same := old.Config.Product == next.Product && old.Config.Endpoint == next.Endpoint && old.Config.AuthType == next.AuthType && old.Config.CAPEM == next.CAPEM && reflect.DeepEqual(old.Config.ApprovedCIDRs, next.ApprovedCIDRs) && old.CollectionMode == mode && old.CollectorAgentID == collector
	if !same {
		c.APIKeySecretName, c.UsernameSecretName, c.PasswordSecretName = "", "", ""
		c.BearerTokenSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName = "", "", ""
	} else {
		c.APIKeySecretName, c.UsernameSecretName, c.PasswordSecretName = old.APIKeySecretName, old.UsernameSecretName, old.PasswordSecretName
		c.BearerTokenSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName = old.BearerTokenSecretName, old.ClientCertificateSecretName, old.ClientKeySecretName
	}
	if req.UsernameSecretName != nil && strings.TrimSpace(*req.UsernameSecretName) != "" && strings.TrimSpace(*req.UsernameSecretName) != c.UsernameSecretName {
		c.PasswordSecretName = ""
	}
	for _, v := range []struct {
		in  *string
		out *string
	}{{req.APIKeySecretName, &c.APIKeySecretName}, {req.UsernameSecretName, &c.UsernameSecretName}, {req.PasswordSecretName, &c.PasswordSecretName}, {req.BearerTokenSecretName, &c.BearerTokenSecretName}, {req.ClientCertificateSecretName, &c.ClientCertificateSecretName}, {req.ClientKeySecretName, &c.ClientKeySecretName}} {
		if v.in != nil && strings.TrimSpace(*v.in) != "" {
			*v.out = strings.TrimSpace(*v.in)
		}
	}
	kibanaCredentials, logstashCredentials := old.KibanaCredentials, old.LogstashCredentials
	componentExecutorChanged := old.Config.Product != next.Product || old.CollectionMode != mode || old.CollectorAgentID != collector
	if componentExecutorChanged || old.Config.KibanaCAPEM != next.KibanaCAPEM || !reflect.DeepEqual(old.Config.KibanaApprovedCIDRs, next.KibanaApprovedCIDRs) {
		kibanaCredentials = storage.ComponentSecretReferences{AuthType: old.KibanaCredentials.AuthType}
	}
	if componentExecutorChanged || old.Config.LogstashCAPEM != next.LogstashCAPEM || !reflect.DeepEqual(old.Config.LogstashApprovedCIDRs, next.LogstashApprovedCIDRs) {
		logstashCredentials = storage.ComponentSecretReferences{AuthType: old.LogstashCredentials.AuthType}
	}
	c.KibanaCredentials = applyElasticComponentCredentials(kibanaCredentials, old.Config.KibanaEndpoint, next.KibanaEndpoint, req.KibanaCredentials, req.KibanaAuthType, req.KibanaAPIKeySecretName, req.KibanaUsernameSecretName, req.KibanaPasswordSecretName, req.KibanaBearerTokenSecretName, req.KibanaClientCertificateSecretName, req.KibanaClientKeySecretName)
	c.LogstashCredentials = applyElasticComponentCredentials(logstashCredentials, old.Config.LogstashEndpoint, next.LogstashEndpoint, req.LogstashCredentials, req.LogstashAuthType, req.LogstashAPIKeySecretName, req.LogstashUsernameSecretName, req.LogstashPasswordSecretName, req.LogstashBearerTokenSecretName, req.LogstashClientCertificateSecretName, req.LogstashClientKeySecretName)
	c.Config = next.Defaults()
	c.Product = c.Config.Product
	c.Endpoint = c.Config.Endpoint
	c.AuthType = c.Config.AuthType
	c.CollectionMode, c.CollectorAgentID = mode, collector
}

func (s *Server) validateElasticConnectionMutation(w http.ResponseWriter, r *http.Request, c, old *storage.ELKConnection) bool {
	for _, name := range elasticSnapshotSecretNames(c.Snapshot()) {
		if name != "" {
			if _, err := normalizeProjectSecretName(name); err != nil {
				writeError(w, 400, "credentials require valid logical project secret names")
				return false
			}
		}
	}
	if c.Config.Endpoint == "" {
		return true
	}
	configured := c.Config.Defaults()
	policy := elasticPolicyConfig(configured)
	if configured.RequestsPerMinute > policy.RequestsPerMinute || configured.MaxRequestsPerPoll > policy.MaxRequestsPerPoll || configured.MaxPollSeconds > policy.MaxPollSeconds {
		writeError(w, 400, "Elastic request budgets exceed deployment policy")
		return false
	}
	trustChanged := false
	targets := []struct {
		endpoint    string
		cidrs       []string
		oldEndpoint string
		oldCIDRs    []string
	}{{c.Config.Endpoint, c.Config.ApprovedCIDRs, "", nil}, {c.Config.KibanaEndpoint, c.Config.KibanaApprovedCIDRs, "", nil}, {c.Config.LogstashEndpoint, c.Config.LogstashApprovedCIDRs, "", nil}}
	if old != nil {
		targets[0].oldEndpoint, targets[0].oldCIDRs = old.Config.Endpoint, old.Config.ApprovedCIDRs
		targets[1].oldEndpoint, targets[1].oldCIDRs = old.Config.KibanaEndpoint, old.Config.KibanaApprovedCIDRs
		targets[2].oldEndpoint, targets[2].oldCIDRs = old.Config.LogstashEndpoint, old.Config.LogstashApprovedCIDRs
	}
	for _, target := range targets {
		trustChanged = trustChanged || len(target.cidrs) > 0 && (target.endpoint != target.oldEndpoint || !reflect.DeepEqual(target.cidrs, target.oldCIDRs))
	}
	if trustChanged {
		user, _ := AuthenticatedUserFromContext(r.Context())
		if s.authSettingsForRequest().Mode != authModeDev && (user == nil || !user.hasGlobalRole(RoleSystemAdmin)) {
			writeError(w, 403, "system administrator approval is required for private monitoring egress")
			return false
		}
	}

	verified := old != nil && old.VerifiedRevision == old.Revision && old.LastCheckStatus == "connected" && reflect.DeepEqual(old.Config, c.Config) && old.CollectionMode == c.CollectionMode && old.CollectorAgentID == c.CollectorAgentID && old.APIKeySecretName == c.APIKeySecretName && old.UsernameSecretName == c.UsernameSecretName && old.PasswordSecretName == c.PasswordSecretName && old.BearerTokenSecretName == c.BearerTokenSecretName && old.ClientCertificateSecretName == c.ClientCertificateSecretName && old.ClientKeySecretName == c.ClientKeySecretName && old.KibanaCredentials == c.KibanaCredentials && old.LogstashCredentials == c.LogstashCredentials
	if c.Enabled && !verified {
		writeError(w, 409, "save disabled and verify the saved Elastic configuration before enabling")
		return false
	}
	return true
}

func resolveElasticCredentials(ctx context.Context, resolver engine.SecretResolver, c *storage.ExpandedElasticConnectionSnapshot) (elastic.Credentials, error) {
	var out elastic.Credentials
	if c == nil {
		return out, errors.New("Elastic connection snapshot unavailable")
	}
	for _, v := range []struct {
		name  string
		value *string
	}{{c.APIKeySecretName, &out.APIKey}, {c.UsernameSecretName, &out.Username}, {c.PasswordSecretName, &out.Password}, {c.BearerTokenSecretName, &out.BearerToken}, {c.ClientCertificateSecretName, &out.ClientCertPEM}, {c.ClientKeySecretName, &out.ClientKeyPEM}} {
		if v.name == "" {
			continue
		}
		value, found := engine.ResolveSecret(ctx, resolver, v.name)
		if !found {
			return elastic.Credentials{}, errors.New("Elastic project credential unavailable")
		}
		*v.value = value
	}
	for _, v := range []struct {
		references  storage.ComponentSecretReferences
		credentials *elastic.ComponentCredentials
	}{{c.KibanaCredentials, &out.Kibana}, {c.LogstashCredentials, &out.Logstash}} {
		v.credentials.AuthType = v.references.AuthType
		for _, field := range []struct {
			name  string
			value *string
		}{{v.references.APIKeySecretName, &v.credentials.APIKey}, {v.references.UsernameSecretName, &v.credentials.Username}, {v.references.PasswordSecretName, &v.credentials.Password}, {v.references.BearerTokenSecretName, &v.credentials.BearerToken}, {v.references.ClientCertificateSecretName, &v.credentials.ClientCertPEM}, {v.references.ClientKeySecretName, &v.credentials.ClientKeyPEM}} {
			if field.name == "" {
				continue
			}
			value, found := engine.ResolveSecret(ctx, resolver, field.name)
			if !found {
				*v.credentials = elastic.ComponentCredentials{}
				break
			}
			*field.value = value
		}
	}
	return out, nil
}

func (s *Server) populateElasticGrant(ctx context.Context, grant *collectoragent.RunJob, job storage.RunMonitorJob) error {
	if job.ElasticConnectionSnapshot == nil || job.Source.Elastic == nil || job.ProjectID != job.ElasticConnectionSnapshot.ProjectID {
		return errors.New("Elastic job snapshot unavailable")
	}
	snapshot := job.ElasticConnectionSnapshot
	caps := snapshot.Capabilities
	grant.Elastic = &elastic.JobConfig{Config: snapshot.Config, Selection: *job.Source.Elastic, Capabilities: &caps}
	grant.Elastic.Selection.ProjectID = job.ProjectID
	grant.Elastic.Selection.QueryOffset = job.QueryOffset
	grant.Elastic.ResolvedIndices = job.ElasticResolvedIndices
	grant.Elastic.DisabledQueries = job.TruncatedQueries
	grant.Elastic.LastHealthAt = elasticHealthWatermark(job)
	grant.Elastic.HealthCoverage = elasticHealthCoverage(job)
	grant.Elastic.FinalHealthAfter = elasticFinalHealthAfter(job)
	grant.Elastic.TerminalWindows = s.elasticTerminalWindows(job)
	grant.Elastic.Continuation = job.ElasticContinuation
	grant.Elastic.ObservedTraceIDs, grant.Elastic.CorrelationLimitReached = s.elasticObservedTraceIDs(job.RunID)
	grant.Elastic.IngestionFrom = elasticIngestionFrom(job)
	grant.QueryFromUTC, grant.QueryUntilUTC = expandedElasticQueryWindow(job, time.Now().UTC())
	grant.LoadEndedAt, grant.DrainDeadline, grant.QueryWatermark = job.LoadEndedAt, job.DrainDeadline, job.QueryWatermark
	grant.Reconciling = job.State == "reconciling"
	grant.MaxBatches = 32
	grant.Destination = snapshot.Config.Endpoint
	credentials, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), snapshot)
	if err != nil {
		return err
	}
	config, err := s.elasticQuotaConfig(ctx, snapshot.Config, "collector:"+job.Source.CollectorID)
	if err != nil {
		return err
	}
	grant.Elastic.Config = config
	key, err := base64.StdEncoding.DecodeString(job.Source.CollectorPublicKey)
	if err != nil {
		return errors.New("collector encryption identity unavailable")
	}
	grant.Credentials, err = collectoragent.SealCredentials(*grant, key, collectoragent.ElasticCredentialBundle(credentials))
	return err
}

func elasticSnapshotSecretNames(c *storage.ExpandedElasticConnectionSnapshot) []string {
	if c == nil {
		return nil
	}
	out := []string{c.APIKeySecretName, c.UsernameSecretName, c.PasswordSecretName, c.BearerTokenSecretName, c.ClientCertificateSecretName, c.ClientKeySecretName}
	for _, v := range []storage.ComponentSecretReferences{c.KibanaCredentials, c.LogstashCredentials} {
		out = append(out, v.APIKeySecretName, v.UsernameSecretName, v.PasswordSecretName, v.BearerTokenSecretName, v.ClientCertificateSecretName, v.ClientKeySecretName)
	}
	return out
}

func applyElasticComponentCredentials(old storage.ComponentSecretReferences, oldEndpoint, nextEndpoint string, nested *storage.ComponentSecretReferences, auth, api, user, password, bearer, cert, key *string) storage.ComponentSecretReferences {
	next := old
	if nextEndpoint == "" {
		return storage.ComponentSecretReferences{}
	}
	if oldEndpoint != nextEndpoint {
		next = storage.ComponentSecretReferences{}
	}
	if nested != nil {
		auth = &nested.AuthType
		api = &nested.APIKeySecretName
		user = &nested.UsernameSecretName
		password = &nested.PasswordSecretName
		bearer = &nested.BearerTokenSecretName
		cert = &nested.ClientCertificateSecretName
		key = &nested.ClientKeySecretName
	}
	if auth != nil && *auth != "" && *auth != next.AuthType {
		next = storage.ComponentSecretReferences{AuthType: strings.TrimSpace(*auth)}
	}
	if user != nil && *user != "" && *user != next.UsernameSecretName {
		next.PasswordSecretName = ""
	}
	for _, v := range []struct {
		in  *string
		out *string
	}{{api, &next.APIKeySecretName}, {user, &next.UsernameSecretName}, {password, &next.PasswordSecretName}, {bearer, &next.BearerTokenSecretName}, {cert, &next.ClientCertificateSecretName}, {key, &next.ClientKeySecretName}} {
		if v.in != nil && *v.in != "" {
			*v.out = strings.TrimSpace(*v.in)
		}
	}
	return next
}
