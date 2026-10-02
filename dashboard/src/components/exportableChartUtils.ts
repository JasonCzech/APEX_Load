export interface ChartExportColumn<T> {
  key: string
  label: string
  value: (row: T) => unknown
}

export interface ChartCSVLimits {
  maxRows?: number
  maxColumns?: number
  maxBytes?: number
}

const DEFAULT_MAX_ROWS = 100_000
const DEFAULT_MAX_COLUMNS = 256
const DEFAULT_MAX_BYTES = 16 * 1024 * 1024

function csvCell(value: unknown): string {
  if (value === null || value === undefined || typeof value === 'number' && !Number.isFinite(value)) return ''
  let text = typeof value === 'object' ? JSON.stringify(value) : String(value)
  if (typeof value === 'string' && /^[\s\uFEFF]*[=+\-@]/.test(text)) {
    text = `'${text}`
  }
  return `"${text.replace(/"/g, '""')}"`
}

export function buildChartCSV<T>(rows: readonly T[], columns: readonly ChartExportColumn<T>[], limits: ChartCSVLimits = {}): string {
  const maxRows = limits.maxRows ?? DEFAULT_MAX_ROWS
  const maxColumns = limits.maxColumns ?? DEFAULT_MAX_COLUMNS
  const maxBytes = limits.maxBytes ?? DEFAULT_MAX_BYTES
  if (rows.length > maxRows) throw new Error(`Chart CSV export exceeds the ${maxRows} row limit.`)
  if (columns.length > maxColumns) throw new Error(`Chart CSV export exceeds the ${maxColumns} column limit.`)

  const encoder = new TextEncoder()
  const lines: string[] = []
  let bytes = encoder.encode('\uFEFF').byteLength
  const append = (line: string) => {
    bytes += encoder.encode(`${line}\r\n`).byteLength
    if (bytes > maxBytes) throw new Error(`Chart CSV export exceeds the ${maxBytes} byte limit.`)
    lines.push(line)
  }
  append(columns.map(column => csvCell(column.label)).join(','))
  for (const row of rows) append(columns.map(column => csvCell(column.value(row))).join(','))
  return `\uFEFF${lines.join('\r\n')}\r\n`
}

export function buildChartSVG(svg: SVGSVGElement, title: string, description: string, maxBytes = DEFAULT_MAX_BYTES): string {
  const originals = [svg, ...svg.querySelectorAll<SVGElement>('*')]
  if (originals.length > 100_000) throw new Error('Chart SVG export exceeds the element limit.')
  const clone = svg.cloneNode(true) as SVGSVGElement
  const copies = [clone, ...clone.querySelectorAll<SVGElement>('*')]
  // Resolve theme paints before detaching, while retaining local gradients.
  originals.forEach((element, index) => {
    const style = window.getComputedStyle(element)
    for (const property of ['fill', 'stroke', 'color', 'font-family', 'font-size', 'font-weight', 'opacity', 'stop-color', 'stop-opacity']) {
      const value = style.getPropertyValue(property)
      if (value && !value.includes('var(')) copies[index].style.setProperty(property, value)
    }
  })
  clone.querySelectorAll('script,foreignObject,iframe,object,embed,image,style,animate,animateMotion,animateTransform,set').forEach(element => element.remove())
  for (const element of [clone, ...clone.querySelectorAll('*')]) {
    for (const attribute of [...element.attributes]) {
      const name = attribute.name.toLowerCase()
      const externalURL = [...attribute.value.matchAll(/url\(([^)]*)\)/gi)].some(match => !match[1].trim().replace(/^['"]|['"]$/g, '').startsWith('#'))
      if (name.startsWith('on') || (name === 'href' || name === 'xlink:href') && !attribute.value.startsWith('#') || externalURL || attribute.value.includes('var(')) element.removeAttribute(attribute.name)
    }
  }
  clone.style.removeProperty('display')
  clone.style.removeProperty('visibility')
  clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  clone.setAttribute('role', 'img')
  clone.setAttribute('aria-label', description)
  if (!clone.getAttribute('width') || clone.getAttribute('width') === '100%') {
    clone.setAttribute('width', String(Math.max(1, Math.round(svg.getBoundingClientRect().width || 800))))
  }
  if (!clone.getAttribute('height') || clone.getAttribute('height') === '100%') {
    clone.setAttribute('height', String(Math.max(1, Math.round(svg.getBoundingClientRect().height || 400))))
  }
  const titleNode = document.createElementNS('http://www.w3.org/2000/svg', 'title')
  titleNode.textContent = title
  const descriptionNode = document.createElementNS('http://www.w3.org/2000/svg', 'desc')
  descriptionNode.textContent = description
  clone.insertBefore(descriptionNode, clone.firstChild)
  clone.insertBefore(titleNode, clone.firstChild)
  const serialized = `<?xml version="1.0" encoding="UTF-8"?>\n${new XMLSerializer().serializeToString(clone)}`
  if (new TextEncoder().encode(serialized).byteLength > maxBytes) {
    throw new Error(`Chart SVG export exceeds the ${maxBytes} byte limit.`)
  }
  return serialized
}
