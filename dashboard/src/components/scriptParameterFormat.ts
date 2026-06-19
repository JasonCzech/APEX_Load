import type { ScriptParameterDefinition } from '../types/scripts'

type ParameterConfig = ScriptParameterDefinition

// Parameter format presets + type-default helpers, shared by ScriptEditor and its
// extracted Parameter* UI components.

export interface ParameterFormatPreset {
  description: string
  label: string
  value: string
  timezone?: string
}

export const CUSTOM_PARAMETER_FORMAT_VALUE = '__custom__'

export const TIMESTAMP_PARAMETER_FORMAT_PRESETS: ParameterFormatPreset[] = [
  { value: 'unix', label: 'Unix seconds', description: 'Epoch timestamp in seconds.' },
  { value: 'unix_ms', label: 'Unix milliseconds', description: 'Epoch timestamp in milliseconds.' },
  { value: 'iso8601', label: 'ISO 8601', description: 'Standard API timestamp with timezone.' },
  { value: 'rfc3339', label: 'RFC 3339', description: 'RFC 3339 timestamp with seconds.' },
  { value: 'rfc3339_nano', label: 'RFC 3339 nano', description: 'RFC 3339 timestamp with nanosecond precision.' },
]

export const DATE_TIME_PARAMETER_FORMAT_PRESETS: ParameterFormatPreset[] = [
  { value: '2006-01-02 15:04:05', label: 'SQL date time', description: 'YYYY-MM-DD HH:mm:ss.' },
  { value: '2006-01-02T15:04:05Z', label: 'Source UTC ISO', description: 'Source %Y-%m-%dT%H:%M:%SZ.', timezone: 'UTC' },
  { value: '2006-01-02T15:04:05Z07:00', label: 'ISO 8601 offset', description: 'YYYY-MM-DDTHH:mm:ss±hh:mm.' },
  { value: '2006-01-02T15:04:05.000Z07:00', label: 'ISO milliseconds', description: 'ISO timestamp with millisecond precision.' },
  { value: '2006-01-02T15:04:05.000000Z07:00', label: 'ISO microseconds', description: 'ISO timestamp with microsecond precision.' },
  { value: '2006-01-02', label: 'Date only ISO', description: 'YYYY-MM-DD.' },
  { value: '20060102', label: 'Compact date', description: 'YYYYMMDD.' },
  { value: '20060102150405', label: 'Compact date time', description: 'YYYYMMDDHHmmss.' },
  { value: '01/02/2006 15:04:05', label: 'US numeric date time', description: 'MM/DD/YYYY HH:mm:ss.' },
  { value: '02/01/2006 15:04:05', label: 'EU numeric date time', description: 'DD/MM/YYYY HH:mm:ss.' },
  { value: '02-Jan-2006 15:04:05', label: 'Source short month', description: 'DD-Mon-YYYY HH:mm:ss.' },
  { value: 'Monday, January 02, 2006 15:04:05', label: 'Long named date time', description: 'Weekday, Month DD, YYYY HH:mm:ss.' },
  { value: 'Mon Jan _2 15:04:05 MST 2006', label: 'Unix ctime style', description: 'Named day/month with timezone.' },
  { value: 'Mon, 02 Jan 2006 15:04:05 MST', label: 'RFC 1123', description: 'HTTP-date style timestamp.' },
]

export const TIME_PARAMETER_FORMAT_PRESETS: ParameterFormatPreset[] = [
  { value: '15:04:05', label: '24-hour time', description: 'HH:mm:ss.' },
  { value: '15:04', label: '24-hour minute', description: 'HH:mm.' },
  { value: '15:04:05.000', label: '24-hour milliseconds', description: 'HH:mm:ss.SSS.' },
  { value: '03:04:05 PM', label: '12-hour time', description: 'hh:mm:ss AM/PM.' },
  { value: '03:04 PM', label: '12-hour minute', description: 'hh:mm AM/PM.' },
  { value: '15:04:05Z', label: 'UTC suffix time', description: 'HH:mm:ssZ.', timezone: 'UTC' },
  { value: '15:04:05 MST', label: 'Time with zone name', description: 'HH:mm:ss timezone abbreviation.' },
  { value: '15:04:05 -0700', label: 'Time with numeric offset', description: 'HH:mm:ss ±hhmm.' },
]

export const TIMESTAMP_PARAMETER_FORMATS = new Set(TIMESTAMP_PARAMETER_FORMAT_PRESETS.map(preset => preset.value))

export function parameterFormatForType(nextType: string, currentFormat?: string) {
  const format = currentFormat || ''
  if (nextType === 'timestamp') {
    return TIMESTAMP_PARAMETER_FORMATS.has(format) ? format : 'iso8601'
  }
  if (nextType === 'date_time') {
    return format && !TIMESTAMP_PARAMETER_FORMATS.has(format) ? format : '2006-01-02 15:04:05'
  }
  if (nextType === 'time') {
    return format && !TIMESTAMP_PARAMETER_FORMATS.has(format) && format !== '2006-01-02 15:04:05' ? format : '15:04:05'
  }
  return currentFormat
}

export function parameterFormatPresetsForType(type: string): ParameterFormatPreset[] {
  if (type === 'timestamp') return TIMESTAMP_PARAMETER_FORMAT_PRESETS
  if (type === 'time') return TIME_PARAMETER_FORMAT_PRESETS
  if (type === 'date_time') return DATE_TIME_PARAMETER_FORMAT_PRESETS
  return []
}

// Built-in parameter types (everything except the file/list data-backed types) generate their
// value at runtime — random number, iteration counter, time, uuid, etc.
export function isBuiltinParameterType(type?: string | null): boolean {
  return Boolean(type) && type !== 'list' && type !== 'file'
}

// applyParameterTypeDefaults normalizes a parameter when its type changes, seeding the
// type-specific defaults (start/step/length/format) and the structural runtime fields.
export function applyParameterTypeDefaults(current: ParameterConfig, nextType: string): ParameterConfig {
  return {
    ...current,
    type: nextType,
    update: current.update || 'each_iteration',
    selection: current.selection || 'sequential',
    on_exhaustion: current.on_exhaustion || 'wrap',
    start: nextType === 'current_iteration' ? (current.start ?? 1) : current.start,
    step: (nextType === 'counter' || nextType === 'current_iteration') ? (current.step || 1) : current.step,
    length: nextType === 'random_string' ? (current.length || 12) : current.length,
    format: parameterFormatForType(nextType, current.format),
  }
}
