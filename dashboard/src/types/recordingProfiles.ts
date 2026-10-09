// Project recording profiles (pkg/recordingprofile): the saved recording
// options the proxy recorder, the browser recorder's HTTP script and HAR
// imports apply. Grouped like VuGen's Recording Options.

export type RecordingProfileUserRule = {
  name: string
  type: 'boundary' | 'regex'
  left?: string
  right?: string
  regex?: string
  hosts?: string[]
  content_types?: string[]
  disabled?: boolean
}

export type RecordingProfileCorrelation = {
  disabled?: boolean
  disabled_rules?: string[]
  user_rules?: RecordingProfileUserRule[]
  excluded_values?: string[]
  excluded_content_types?: string[]
  min_value_length?: number
  max_value_length?: number
  case_insensitive?: boolean
  prefer_regex?: boolean
}

export type RecordingProfile = {
  id: string
  name: string
  description?: string
  http_script_level?: '' | 'url' | 'html'
  resource_mode?: '' | 'runtime' | 'in_step' | 'separate'
  record_headers?: string[]
  drop_headers?: string[]
  include_hosts?: string[]
  exclude_hosts?: string[]
  exclude_content_types?: string[]
  think_time_threshold_ms?: number
  page_title_checks?: boolean
  async_scan?: boolean
  body_capture_limit_bytes?: number
  capture_cookies?: boolean
  correlation?: RecordingProfileCorrelation
}

export type CorrelationRuleInfo = {
  id: string
  family: string
  title: string
  description: string
  base_confidence: number
}

export type RecordingProfilesResponse = {
  profiles: RecordingProfile[]
  rules: CorrelationRuleInfo[]
  updated_at: string
}
