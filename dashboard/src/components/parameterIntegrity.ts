import type { ScriptDefinition, ScriptDataSource } from '../types/scripts'
import { parseInlineData, stringifyInlineData } from './scriptEditorUtils'

const canonical = (value: string) => value.trim().toLowerCase()
const tokenPattern = /\{\{\s*(param|data|var)\.([^{}]+?)\s*\}\}/g

export interface ParameterUsage { path: string; token: string }

/** Visit executable values only. Imported evidence and dataset contents are immutable evidence here. */
function mapExecutable(script: ScriptDefinition, map: (value: string, path: string) => string): ScriptDefinition {
  const walk = (value: unknown, path: string): unknown => {
    if (typeof value === 'string') return map(value, path)
    if (Array.isArray(value)) return value.map((item, index) => walk(item, `${path}[${index}]`))
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, walk(item, `${path}.${key}`)]))
    return value
  }
  return { ...script, ...Object.fromEntries(['config', 'init', 'actions', 'teardown'].filter(key => key in script).map(key => [key, walk(script[key as keyof ScriptDefinition], key)])) }
}

export function parameterUsages(script: ScriptDefinition, name: string): ParameterUsage[] {
  const usages: ParameterUsage[] = []
  mapExecutable(script, (value, path) => {
    for (const match of value.matchAll(tokenPattern)) {
      if (match[1] === 'param' && canonical(match[2]) === canonical(name)) usages.push({ path, token: match[0] })
    }
    return value
  })
  script.parameters?.forEach((parameter, index) => {
    if (canonical(parameter.same_line_as || '') === canonical(name)) usages.push({ path: `parameters[${index}].same_line_as`, token: name })
  })
  return usages
}

export function renameScriptParameter(script: ScriptDefinition, index: number, name: string): ScriptDefinition {
  const current = script.parameters?.[index]
  if (!current) throw new Error('Parameter no longer exists.')
  const nextName = name.trim()
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(nextName)) throw new Error('Use a letter or underscore followed by letters, numbers, or underscores.')
  if (script.parameters?.some((parameter, other) => other !== index && canonical(parameter.name) === canonical(nextName))) throw new Error('A parameter with that name already exists.')
  let ambiguous = false
  mapExecutable(script, value => {
    for (const match of value.matchAll(tokenPattern)) if (match[1] === 'var' && canonical(match[2]) === canonical(current.name)) ambiguous = true
    return value
  })
  if (ambiguous) throw new Error('This name is also used by a var token. In Source, change parameter references to explicit param tokens before renaming; extracted variables may use the same name.')
  const next = mapExecutable(script, value => value.replace(tokenPattern, (token, namespace: string, expression: string) => namespace === 'param' && canonical(expression) === canonical(current.name) ? `{{param.${nextName}}}` : token))
  next.parameters = script.parameters?.map((parameter, other) => ({ ...parameter, ...(other === index ? { name: nextName } : {}), ...(canonical(parameter.same_line_as || '') === canonical(current.name) ? { same_line_as: nextName } : {}) }))
  return next
}

export function datasetColumnDependencies(script: ScriptDefinition, source: ScriptDataSource, column: string): string[] {
  const dependencies = (script.parameters || []).filter(parameter => canonical(parameter.data_source || '') === canonical(source.name) && (canonical(parameter.column || '') === canonical(column) || (!parameter.column && parseInlineData(source).columns.length === 1))).map(parameter => `Parameter ${parameter.name}`)
  mapExecutable(script, (value, path) => {
    for (const match of value.matchAll(tokenPattern)) {
      if (['data', 'var'].includes(match[1]) && match[2].trim() === `${source.name}.${column}`) dependencies.push(path)
    }
    return value
  })
  return [...new Set(dependencies)]
}

export function renameDatasetColumn(script: ScriptDefinition, sourceIndex: number, columnIndex: number, name: string): ScriptDefinition {
  const source = script.data_sources?.[sourceIndex]
  if (!source) throw new Error('Dataset no longer exists.')
  const { columns, rows } = parseInlineData(source)
  const oldName = columns[columnIndex]
  const nextName = name.trim()
  if (!oldName || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(nextName)) throw new Error('Column names must start with a letter or underscore and contain only letters, numbers, or underscores.')
  if (columns.some((column, index) => index !== columnIndex && canonical(column) === canonical(nextName))) throw new Error('A column with that name already exists.')
  columns[columnIndex] = nextName
  const next = mapExecutable(script, value => value.replace(tokenPattern, (token, namespace: string, expression: string) => ['data', 'var'].includes(namespace) && expression.trim() === `${source.name}.${oldName}` ? `{{${namespace}.${source.name}.${nextName}}}` : token))
  next.data_sources = script.data_sources?.map((item, index) => index === sourceIndex ? { ...source, columns: source.first_row_header === false ? columns : [], inline_data: stringifyInlineData(source, columns, rows) } : item)
  next.parameters = script.parameters?.map(parameter => canonical(parameter.data_source || '') === canonical(source.name) && canonical(parameter.column || '') === canonical(oldName) ? { ...parameter, column: nextName } : parameter)
  return next
}

export function validateDatasetReplacement(script: ScriptDefinition, sourceIndex: number, replacement: ScriptDataSource): void {
  const source = script.data_sources?.[sourceIndex]
  if (!source) throw new Error('Dataset no longer exists.')
  if (source.name !== replacement.name && ((script.parameters || []).some(parameter => parameter.data_source === source.name) || parseInlineData(source).columns.some(column => datasetColumnDependencies(script, source, column).length))) throw new Error('Remap dataset references before changing its name. No data was changed.')
  const before = parseInlineData(source).columns
  const after = parseInlineData(replacement).columns
  const removed = before.filter(column => !after.some(next => next === column))
  const affected = removed.flatMap(column => datasetColumnDependencies(script, source, column))
  if (affected.length) throw new Error(`Remap these references before removing or replacing columns: ${[...new Set(affected)].join(', ')}. No data was changed.`)
}
