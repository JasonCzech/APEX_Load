/** Browser-side timing and resource measurements captured during replay/run execution. */
export interface ClientMetrics {
  device_profile?: string
  device_platform?: string
  device_tier?: string
  browser_engine?: string
  action_duration_ms?: number
  fcp_ms?: number
  lcp_ms?: number
  cls_score?: number
  inp_ms?: number
  ttfb_ms?: number
  dom_content_loaded_ms?: number
  dom_complete_ms?: number
  load_event_ms?: number
  blocking_resource_count?: number
  total_resource_count?: number
  resource_transfer_kb?: number
  js_heap_used_mb?: number
  js_heap_total_mb?: number
  browser_process_cpu_pct?: number
  browser_process_mem_mb?: number
  layout_count?: number
  recalc_style_count?: number
  script_duration_ms?: number
  layout_duration_ms?: number
  task_duration_ms?: number
  long_task_count?: number
}
