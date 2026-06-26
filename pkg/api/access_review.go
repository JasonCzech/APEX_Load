package api

// access_review.go — a read-only access-review report (C4-MVP). It surfaces the
// users and service accounts an admin should look at during a periodic recertification:
// stale / never-signed-in / unbound / disabled users and all system-admin grants, plus
// service accounts with overdue rotation, expiry, broad scopes, or that have never been
// used. Pure aggregation over existing data — no new storage. System-admin gated.

import (
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultAccessReviewStaleDays         = 90
	defaultAccessReviewExpiringDays      = 14
	accessReviewMaxUsers                 = 10000
	defaultAccessReviewDenialWindowHours = 24
	accessReviewDenialScanCap            = 5000
	auditActionAccessDenied              = "auth.access.denied"
)

type AccessReviewUser struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	DisplayName string     `json:"display_name,omitempty"`
	Active      bool       `json:"active"`
	Flags       []string   `json:"flags"`
	GlobalRoles []string   `json:"global_roles,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

type AccessReviewServiceAccount struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Active        bool       `json:"active"`
	Flags         []string   `json:"flags"`
	Scopes        []string   `json:"scopes,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RotationDueAt *time.Time `json:"rotation_due_at,omitempty"`
}

type AccessReviewResponse struct {
	GeneratedAt             time.Time                    `json:"generated_at"`
	StaleAfterDays          int                          `json:"stale_after_days"`
	ExpiringDays            int                          `json:"expiring_within_days"`
	Users                   []AccessReviewUser           `json:"users"`
	ServiceAccounts         []AccessReviewServiceAccount `json:"service_accounts"`
	Summary                 map[string]int               `json:"summary"`
	RecentDenials           int                          `json:"recent_denials"`
	RecentDenialWindowHours int                          `json:"recent_denial_window_hours"`
}

func (s *Server) handleGetAccessReview(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	if !s.currentRequestCanManageUserLifecycle(r) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	review, err := s.computeAccessReview(time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, review)
}

// computeAccessReview gathers users + service accounts, builds the flagged review, and
// folds in a recent auth-denial count. Shared by the on-demand admin endpoint and the
// background notifier so both report the same posture. The denial count is best-effort:
// a query failure leaves it at zero rather than failing the whole report.
func (s *Server) computeAccessReview(now time.Time) (AccessReviewResponse, error) {
	users, err := s.store.ListUsers(storage.UserFilter{Limit: accessReviewMaxUsers})
	if err != nil {
		return AccessReviewResponse{}, err
	}
	accounts, err := s.store.ListServiceAccounts()
	if err != nil {
		return AccessReviewResponse{}, err
	}
	review := buildAccessReview(users, accounts, now)
	windowHours := accessReviewIntEnv("APEXLOAD_ACCESS_REVIEW_DENIAL_WINDOW_HOURS", defaultAccessReviewDenialWindowHours)
	review.RecentDenialWindowHours = windowHours
	if windowHours > 0 {
		since := now.Add(-time.Duration(windowHours) * time.Hour)
		denials, derr := s.store.ListAuditEvents(storage.AuditEventFilter{
			Action:       auditActionAccessDenied,
			Status:       "failure",
			CreatedAfter: &since,
			Limit:        accessReviewDenialScanCap,
		})
		if derr == nil {
			review.RecentDenials = len(denials)
		}
	}
	return review, nil
}

func buildAccessReview(users []*storage.User, accounts []*storage.ServiceAccount, now time.Time) AccessReviewResponse {
	staleDays := accessReviewIntEnv("APEXLOAD_ACCESS_REVIEW_STALE_DAYS", defaultAccessReviewStaleDays)
	expiringDays := accessReviewIntEnv("APEXLOAD_ACCESS_REVIEW_EXPIRING_DAYS", defaultAccessReviewExpiringDays)
	staleBefore := now.AddDate(0, 0, -staleDays)
	expiringBefore := now.AddDate(0, 0, expiringDays)
	summary := map[string]int{}
	addFlag := func(flags []string, flag string) []string {
		summary[flag]++
		return append(flags, flag)
	}

	reviewUsers := make([]AccessReviewUser, 0)
	for _, user := range users {
		if user == nil {
			continue
		}
		flags := []string{}
		globalRoles := globalRolesFromBindings(user.Bindings)
		if !user.Active {
			flags = addFlag(flags, "disabled")
		} else {
			if user.LastSeenAt == nil {
				flags = addFlag(flags, "never_signed_in")
			} else if user.LastSeenAt.Before(staleBefore) {
				flags = addFlag(flags, "stale")
			}
			if len(user.Bindings) == 0 {
				flags = addFlag(flags, "no_bindings")
			}
		}
		if containsRoleString(globalRoles, RoleSystemAdmin) {
			flags = addFlag(flags, "system_admin")
		}
		if len(flags) == 0 {
			continue
		}
		reviewUsers = append(reviewUsers, AccessReviewUser{
			ID: user.ID, Subject: user.Subject, DisplayName: user.DisplayName, Active: user.Active,
			Flags: flags, GlobalRoles: globalRoles, LastSeenAt: user.LastSeenAt,
		})
	}

	reviewAccounts := make([]AccessReviewServiceAccount, 0)
	for _, account := range accounts {
		if account == nil {
			continue
		}
		flags := []string{}
		if !account.Active {
			flags = addFlag(flags, "disabled")
		}
		if account.ExpiresAt != nil {
			if !account.ExpiresAt.After(now) {
				flags = addFlag(flags, "expired")
			} else if account.ExpiresAt.Before(expiringBefore) {
				flags = addFlag(flags, "expiring_soon")
			}
		}
		if account.RotationDueAt != nil && !account.RotationDueAt.After(now) {
			flags = addFlag(flags, "overdue_rotation")
		}
		if account.LastUsedAt == nil {
			flags = addFlag(flags, "never_used")
		}
		if hasBroadServiceAccountScope(account.Scopes) {
			flags = addFlag(flags, "broad_scope")
		}
		if len(flags) == 0 {
			continue
		}
		reviewAccounts = append(reviewAccounts, AccessReviewServiceAccount{
			ID: account.ID, Name: account.Name, Active: account.Active, Flags: flags,
			Scopes: account.Scopes, LastUsedAt: account.LastUsedAt, ExpiresAt: account.ExpiresAt, RotationDueAt: account.RotationDueAt,
		})
	}

	return AccessReviewResponse{
		GeneratedAt: now, StaleAfterDays: staleDays, ExpiringDays: expiringDays,
		Users: reviewUsers, ServiceAccounts: reviewAccounts, Summary: summary,
	}
}

func globalRolesFromBindings(bindings []*storage.UserRoleBinding) []string {
	seen := map[string]struct{}{}
	roles := []string{}
	for _, binding := range bindings {
		if binding == nil || strings.TrimSpace(binding.ScopeType) != "global" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(binding.Role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func containsRoleString(roles []string, target string) bool {
	for _, role := range roles {
		if strings.EqualFold(role, target) {
			return true
		}
	}
	return false
}

func hasBroadServiceAccountScope(scopes []string) bool {
	for _, scope := range scopes {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "*", "automation:*", "read", "write":
			return true
		}
	}
	return false
}

func accessReviewIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
