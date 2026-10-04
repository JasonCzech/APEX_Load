import type { PrometheusQuery } from '../types/prometheus'

export interface PrometheusPresetScope { job: string; namespace?: string }
export interface PrometheusPreset { id: string; name: string; metrics: string[]; help: string; kubernetes?: boolean; build: (selector: string) => PrometheusQuery }

function query(id: string, expression: string, unit: string, semantics: PrometheusQuery['semantics'], labels: string[], metricSelector: string, terminal?: string): PrometheusQuery {
  return { id, name: id, expression, unit, semantics, allowed_labels: labels, min_series: 1, freshness_expression: `min by (${labels.join(', ')}) (timestamp(${metricSelector}))`, max_sample_age_seconds: 90, ...(terminal ? { terminal_expression: terminal } : {}) }
}
const hostLabels = ['job', 'instance']
const podLabels = ['namespace', 'pod']
// Read original timestamps from direct selectors before adding operand identities.
// The minimum covers every retained input to the ratio; one fresh operand cannot
// conceal a stale denominator still reused by Prometheus selector lookback.
function memoryFreshness(selector: string): string {
  const operands = ['node_memory_MemAvailable_bytes', 'node_memory_MemTotal_bytes'].map((metric, index) => `label_replace(timestamp(${metric}{${selector}}), "apex_memory_operand", "${index}", "", "")`)
  return `min by (job, instance) (${operands.join(' or ')})`
}
const rate = (id: string, metric: string, unit: string, labels: string[], selector: string) => query(id, `sum by (${labels.join(', ')}) (rate(${metric}{${selector}}[2m]))`, unit, 'rate', labels, `${metric}{${selector}}`, `sum by (${labels.join(', ')}) (rate(${metric}{${selector}}[{{window.duration}}]))`)

export const prometheusPresets: PrometheusPreset[] = [
  { id: 'host_cpu_percent', name: 'Host CPU usage', metrics: ['node_cpu_seconds_total'], help: 'Percentage of non-idle CPU time per target instance.', build: s => query('host_cpu_percent', `100 * (1 - avg by (job, instance) (rate(node_cpu_seconds_total{${s},mode="idle"}[2m])))`, '%', 'gauge', hostLabels, `node_cpu_seconds_total{${s},mode="idle"}`) },
  { id: 'host_memory_percent', name: 'Host memory usage', metrics: ['node_memory_MemAvailable_bytes', 'node_memory_MemTotal_bytes'], help: 'Memory unavailable to applications as a percentage of total memory.', build: s => ({ ...query('host_memory_percent', `100 * (1 - sum by (job, instance) (node_memory_MemAvailable_bytes{${s}}) / sum by (job, instance) (node_memory_MemTotal_bytes{${s}}))`, '%', 'gauge', hostLabels, `node_memory_MemAvailable_bytes{${s}}`), freshness_expression: memoryFreshness(s) }) },
  { id: 'process_cpu_cores', name: 'Process CPU', metrics: ['process_cpu_seconds_total'], help: 'CPU seconds per second, expressed as CPU cores.', build: s => rate('process_cpu_cores', 'process_cpu_seconds_total', 'cores', hostLabels, s) },
  { id: 'process_memory_bytes', name: 'Process resident memory', metrics: ['process_resident_memory_bytes'], help: 'Maximum observed resident memory per target instance.', build: s => query('process_memory_bytes', `max by (job, instance) (process_resident_memory_bytes{${s}})`, 'bytes', 'gauge', hostLabels, `process_resident_memory_bytes{${s}}`) },
  { id: 'http_request_rate', name: 'HTTP request rate', metrics: ['http_requests_total'], help: 'Calls per second for the selected application job. Review instrumentation semantics.', build: s => rate('http_request_rate', 'http_requests_total', 'requests/s', ['job'], s) },
  { id: 'go_heap_bytes', name: 'Go heap allocation', metrics: ['go_memstats_heap_alloc_bytes'], help: 'Allocated Go heap bytes per target instance.', build: s => query('go_heap_bytes', `max by (job, instance) (go_memstats_heap_alloc_bytes{${s}})`, 'bytes', 'gauge', hostLabels, `go_memstats_heap_alloc_bytes{${s}}`) },
  { id: 'go_gc_seconds_rate', name: 'Go GC pause time', metrics: ['go_gc_duration_seconds_sum'], help: 'GC pause seconds per second. Availability depends on the Go collector version.', build: s => rate('go_gc_seconds_rate', 'go_gc_duration_seconds_sum', 'seconds/s', hostLabels, s) },
  { id: 'jvm_gc_seconds_rate', name: 'JVM GC collection time', metrics: ['jvm_gc_collection_seconds_sum'], help: 'GC collection seconds per second for this instrumentation family.', build: s => rate('jvm_gc_seconds_rate', 'jvm_gc_collection_seconds_sum', 'seconds/s', hostLabels, s) },
  { id: 'pod_cpu_cores', name: 'Kubernetes pod CPU', metrics: ['container_cpu_usage_seconds_total'], help: 'Application-container CPU cores per namespace and pod; excludes infrastructure containers.', kubernetes: true, build: s => rate('pod_cpu_cores', 'container_cpu_usage_seconds_total', 'cores', podLabels, `${s},container!="",container!="POD"`) },
  { id: 'pod_memory_bytes', name: 'Kubernetes pod working memory', metrics: ['container_memory_working_set_bytes'], help: 'Sum of application-container working memory per pod; review duplicate scrape sources.', kubernetes: true, build: s => query('pod_memory_bytes', `sum by (namespace, pod) (container_memory_working_set_bytes{${s},container!="",container!="POD"})`, 'bytes', 'gauge', podLabels, `container_memory_working_set_bytes{${s},container!="",container!="POD"}`) },
  { id: 'pod_restart_count', name: 'Kubernetes pod restarts', metrics: ['kube_pod_container_status_restarts_total'], help: 'Two-minute restart increase for display, with an optional exact-window restart total.', kubernetes: true, build: s => query('pod_restart_count', `sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{${s}}[2m]))`, 'count', 'count', podLabels, `kube_pod_container_status_restarts_total{${s}}`, `sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{${s}}[{{window.duration}}]))`) },
]

export function prometheusPresetAvailable(preset: PrometheusPreset, metrics: string[]): boolean { const names = new Set(metrics); return preset.metrics.every(metric => names.has(metric)) }

export function buildPrometheusPreset(preset: PrometheusPreset, scope: PrometheusPresetScope): PrometheusQuery | undefined {
  const job = scope.job.trim(), namespace = scope.namespace?.trim() || ''
  if (!job || job.length > 128 || namespace.length > 128 || /[\r\n\0]/.test(job + namespace) || preset.kubernetes && !namespace) return undefined
  const selector = `job=${JSON.stringify(job)}${namespace ? `,namespace=${JSON.stringify(namespace)}` : ''}`
  return preset.build(selector)
}
