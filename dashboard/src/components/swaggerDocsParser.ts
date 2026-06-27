export type SwaggerParameter = {
  name: string
  location: string
  required: boolean
  type: string
}

export type SwaggerResponse = {
  status: string
  description: string
}

export type SwaggerOperation = {
  method: string
  path: string
  category: string
  summary: string
  description: string
  operationId: string
  roles: string
  tags: string[]
  parameters: SwaggerParameter[]
  responses: SwaggerResponse[]
}

export type SwaggerDocument = {
  title: string
  description: string
  version: string
  serverUrl: string
  operations: SwaggerOperation[]
}

const HTTP_METHODS = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])

const CATEGORY_BY_PATH_SEGMENT: Record<string, string> = {
  ai: 'AI Assistance',
  'application-repository': 'Workspace & Assets',
  applications: 'Workspace & Assets',
  archives: 'Results & Reporting',
  auth: 'Identity & Access',
  'browser-recorder': 'Recording & Replay',
  'browser-replay': 'Recording & Replay',
  capacity: 'Scheduling',
  'correlation-rulesets': 'Script Authoring',
  correlations: 'Script Authoring',
  'data-pools': 'Data & Parameters',
  desktop: 'System & Distribution',
  domains: 'Workspace & Assets',
  downloads: 'System & Distribution',
  dsl: 'Script Authoring',
  'execution-plans': 'Test Execution',
  fleet: 'Operations & Observability',
  folders: 'Workspace & Assets',
  integrations: 'Integrations & Protocols',
  library: 'Script Library',
  metrics: 'Operations & Observability',
  monitoring: 'Operations & Observability',
  'object-repositories': 'Script Authoring',
  projects: 'Workspace & Assets',
  'protocol-bundles': 'Integrations & Protocols',
  protocols: 'Integrations & Protocols',
  recorder: 'Recording & Replay',
  releases: 'Results & Reporting',
  remotes: 'Remote Execution',
  replay: 'Recording & Replay',
  reports: 'Results & Reporting',
  results: 'Results & Reporting',
  'run-calendar': 'Scheduling',
  'runtime-settings': 'Execution Configuration',
  scenarios: 'Test Execution',
  schedules: 'Scheduling',
  'script-debug': 'Recording & Replay',
  scripts: 'Script Authoring',
  selectors: 'Script Authoring',
  system: 'System & Distribution',
  tests: 'Test Execution',
  ws: 'Operations & Observability',
}

function countLeadingSpaces(line: string) {
  return line.match(/^ */)?.[0].length || 0
}

function cleanYamlValue(value: string) {
  let next = value.trim()
  if (next.startsWith("'")) next = next.slice(1)
  if (next.endsWith("'")) next = next.slice(0, -1)
  return next
    .replace(/''/g, "'")
    .replace(/\r/g, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

function humanizeSegment(segment: string) {
  return segment
    .split(/[-_]+/)
    .filter(Boolean)
    .map(word => word.toUpperCase() === word ? word : word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

function categoryForPath(path: string) {
  if (path === '/metrics') return CATEGORY_BY_PATH_SEGMENT.metrics
  const trimmed = path.replace(/^\/api\/v1\/?/, '').replace(/^\//, '')
  const segment = trimmed.split('/')[0]
  if (!segment) return 'General'
  return CATEGORY_BY_PATH_SEGMENT[segment] || humanizeSegment(segment)
}

function readBlockScalar(lines: string[], startIndex: number, fieldIndent: number) {
  const line = lines[startIndex]
  const firstValue = line.slice(line.indexOf(':') + 1).trim()
  const parts = /^[>|][+-]?$/.test(firstValue) ? [] : [firstValue]
  let index = startIndex + 1
  while (index < lines.length) {
    const nextLine = lines[index]
    const trimmed = nextLine.trim()
    if (trimmed) {
      const indent = countLeadingSpaces(nextLine)
      const isNextField = indent <= fieldIndent && /^[A-Za-z0-9_-]+:/.test(trimmed)
      const isNextMethod = indent <= 4 && HTTP_METHODS.has(trimmed.replace(':', '').toLowerCase())
      const isNextPath = indent <= 2 && trimmed.startsWith('/')
      if (isNextField || isNextMethod || isNextPath) {
        break
      }
    }
    parts.push(trimmed)
    index += 1
  }
  return cleanYamlValue(parts.join('\n'))
}

function readField(lines: string[], fieldName: string, fieldIndent: number) {
  const prefix = `${' '.repeat(fieldIndent)}${fieldName}:`
  const index = lines.findIndex(line => line.startsWith(prefix))
  if (index < 0) return ''
  return readBlockScalar(lines, index, fieldIndent)
}

function parseTags(lines: string[]) {
  const tags: string[] = []
  const tagIndex = lines.findIndex(line => /^ {6}tags:\s*$/.test(line))
  if (tagIndex < 0) return tags
  for (let index = tagIndex + 1; index < lines.length; index += 1) {
    const line = lines[index]
    const trimmed = line.trim()
    const indent = countLeadingSpaces(line)
    if (!trimmed) continue
    if (indent <= 6 && !trimmed.startsWith('-')) break
    const match = trimmed.match(/^-\s+(.+)$/)
    if (!match) continue
    tags.push(cleanYamlValue(match[1]))
  }
  return tags
}

function parseParameters(lines: string[]) {
  const parameters: SwaggerParameter[] = []
  const start = lines.findIndex(line => /^ {6}parameters:\s*$/.test(line))
  if (start < 0) return parameters
  let current: SwaggerParameter | null = null
  for (let index = start + 1; index < lines.length; index += 1) {
    const line = lines[index]
    const trimmed = line.trim()
    const indent = countLeadingSpaces(line)
    if (!trimmed) continue
    if (indent <= 6 && !trimmed.startsWith('-')) break
    const nameMatch = trimmed.match(/^-\s+name:\s*(.+)$/)
    if (nameMatch) {
      current = { name: cleanYamlValue(nameMatch[1]), location: '', required: false, type: '' }
      parameters.push(current)
      continue
    }
    if (!current) continue
    const locationMatch = trimmed.match(/^in:\s*(.+)$/)
    if (locationMatch) current.location = cleanYamlValue(locationMatch[1])
    const requiredMatch = trimmed.match(/^required:\s*(.+)$/)
    if (requiredMatch) current.required = cleanYamlValue(requiredMatch[1]).toLowerCase() === 'true'
    const typeMatch = trimmed.match(/^type:\s*(.+)$/)
    if (typeMatch) current.type = cleanYamlValue(typeMatch[1])
  }
  return parameters
}

function parseResponses(lines: string[]) {
  const responses: SwaggerResponse[] = []
  const start = lines.findIndex(line => /^ {6}responses:\s*$/.test(line))
  if (start < 0) return responses
  let current: SwaggerResponse | null = null
  for (let index = start + 1; index < lines.length; index += 1) {
    const line = lines[index]
    const trimmed = line.trim()
    const indent = countLeadingSpaces(line)
    if (!trimmed) continue
    if (indent <= 6 && !trimmed.startsWith('-')) break
    const statusMatch = trimmed.match(/^['"]?([^:'"]+)['"]?:\s*$/)
    if (statusMatch) {
      current = { status: statusMatch[1], description: '' }
      responses.push(current)
      continue
    }
    if (!current) continue
    const descriptionMatch = trimmed.match(/^description:\s*(.+)$/)
    if (descriptionMatch) current.description = cleanYamlValue(descriptionMatch[1])
  }
  return responses
}

function parseOperation(path: string, method: string, lines: string[]): SwaggerOperation {
  const summary = readField(lines, 'summary', 6) || `${method.toUpperCase()} ${path}`
  const tags = parseTags(lines)
  return {
    method: method.toUpperCase(),
    path,
    category: categoryForPath(path),
    summary,
    description: readField(lines, 'description', 6),
    operationId: readField(lines, 'operationId', 6),
    roles: readField(lines, 'x-roles', 6),
    tags,
    parameters: parseParameters(lines),
    responses: parseResponses(lines),
  }
}

export function parseSwaggerDocument(source: string): SwaggerDocument {
  const lines = source.split('\n')
  const title = cleanYamlValue(lines.find(line => /^ {2}title:\s+/.test(line))?.replace(/^ {2}title:\s+/, '') || 'APEX Load API')
  const version = cleanYamlValue(lines.find(line => /^ {2}version:\s+/.test(line))?.replace(/^ {2}version:\s+/, '') || '')
  const serverUrl = cleanYamlValue(lines.find(line => /^\s*-\s+url:\s+/.test(line))?.replace(/^\s*-\s+url:\s+/, '') || '')
  const descriptionIndex = lines.findIndex(line => /^ {2}description:\s+/.test(line))
  const description = descriptionIndex >= 0 ? readBlockScalar(lines, descriptionIndex, 2) : ''
  const operations: SwaggerOperation[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const pathMatch = lines[index].match(/^ {2}(\/.+):\s*$/)
    if (!pathMatch) continue
    const path = pathMatch[1]
    let cursor = index + 1
    while (cursor < lines.length) {
      const methodMatch = lines[cursor].match(/^ {4}([a-z]+):\s*$/)
      if (!methodMatch) {
        if (/^ {2}\/.+:\s*$/.test(lines[cursor])) break
        cursor += 1
        continue
      }
      const method = methodMatch[1]
      if (!HTTP_METHODS.has(method)) {
        cursor += 1
        continue
      }
      const operationStart = cursor + 1
      let operationEnd = operationStart
      while (operationEnd < lines.length && !/^ {4}[a-z]+:\s*$/.test(lines[operationEnd]) && !/^ {2}\/.+:\s*$/.test(lines[operationEnd])) {
        operationEnd += 1
      }
      operations.push(parseOperation(path, method, lines.slice(operationStart, operationEnd)))
      cursor = operationEnd
      if (cursor < lines.length && /^ {2}\/.+:\s*$/.test(lines[cursor])) break
    }
  }

  return { title, description, version, serverUrl, operations }
}
