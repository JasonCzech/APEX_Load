function formatPathKey(key: string): string {
  return /^[a-zA-Z_$][a-zA-Z0-9_$]*$/.test(key) ? `.${key}` : `[${JSON.stringify(key)}]`
}

export function collectJsonViewerVisiblePaths(data: unknown, filter: string): Set<string> | null {
  const lowerFilter = filter.toLowerCase()
  if (!lowerFilter) return null
  const paths = new Set<string>()

  function addSubtree(value: unknown, currentPath: string) {
    paths.add(currentPath)
    if (Array.isArray(value)) value.forEach((child, index) => addSubtree(child, `${currentPath}[${index}]`))
    else if (value !== null && typeof value === 'object') Object.entries(value).forEach(([key, child]) => addSubtree(child, `${currentPath}${formatPathKey(key)}`))
  }
  function traverse(value: unknown, currentPath: string, name?: string): boolean {
    let matches = false
    if (name && name.toLowerCase().includes(lowerFilter)) {
      addSubtree(value, currentPath)
      return true
    }
    if (value === null) matches = 'null'.includes(lowerFilter)
    else if (Array.isArray(value)) value.forEach((child, index) => { if (traverse(child, `${currentPath}[${index}]`)) matches = true })
    else if (typeof value === 'object') Object.entries(value).forEach(([key, child]) => { if (traverse(child, `${currentPath}${formatPathKey(key)}`, key)) matches = true })
    else if (String(value).toLowerCase().includes(lowerFilter)) matches = true
    if (matches) paths.add(currentPath)
    return matches
  }
  traverse(data, '$')
  return paths
}
