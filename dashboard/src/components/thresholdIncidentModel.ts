export type Incident = {
 id: string; project_id: string; test_id: string; test_name: string; key: string; severity: 'warning' | 'danger'
 payload: { metric?: string; value?: number; threshold?: number; transaction?: string; action?: string }
 active: boolean; first_seen_at: string; last_seen_at: string; resolved_at?: string; acknowledged_by?: string; acknowledged_at?: string
}
export type IncidentResponse = { incidents: Incident[]; limit: number; offset: number; global_policy_count: number; last_evaluated_at: string | null; generated_at: string; persistence_warning?: string; active_count: number; unacknowledged_count: number; danger_count: number }
export type IncidentHealth = { severity: 'healthy' | 'warning' | 'danger' | 'unknown'; label: string; summary: string }
export function incidentHealth(data: IncidentResponse | null, error: string | null, now = Date.now()): IncidentHealth {
 if (error || !data) return { severity: 'unknown', label: error ? 'History unavailable' : 'Checking coverage', summary: error || 'Waiting for policy and evaluation data.' }
 if (data.persistence_warning) return { severity: 'warning', label: 'History incomplete', summary: data.persistence_warning }
 if (data.active_count > 0) return { severity: data.danger_count ? 'danger' : 'warning', label: `${data.active_count} active breach${data.active_count === 1 ? '' : 'es'}`, summary: `${data.unacknowledged_count} require acknowledgement. Acknowledgement does not resolve an active breach.` }
 if (!data.global_policy_count) return { severity: 'unknown', label: 'Not configured', summary: 'No global SLA policies are enabled. Test-specific quality gates are separate.' }
 const at = data.last_evaluated_at ? Date.parse(data.last_evaluated_at) : NaN
 if (!Number.isFinite(at) || now - at > 30_000 || at > now + 5000) return { severity: 'unknown', label: 'Waiting for samples', summary: 'No recent evaluation sample for this project. Start or inspect a run to verify coverage.' }
 return { severity: 'healthy', label: 'No breaches observed', summary: 'Enabled policies were recently evaluated. Historical incidents are retained below.' }
}

