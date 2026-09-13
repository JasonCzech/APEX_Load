import type { ScriptActionDefinition } from '../types/scripts'
export function bodyLanguage(action: ScriptActionDefinition): 'json' | 'xml' | 'text' {
  if (action.body_encoding) return 'text'
  const contentType = [...Object.entries(action.headers || {}), ...(action.header_list || []).map(h => [h.name, h.value])].find(([name]) => name.toLowerCase() === 'content-type')?.[1] || ''
  if (/json/i.test(contentType) || /^\s*(?:\[|{)/.test(action.body || '')) return 'json'
  if (/xml/i.test(contentType) || /^\s*</.test(action.body || '')) return 'xml'
  return 'text'
}

export function bodyTemplateReferences(body: string) {
  return Array.from(new Set(Array.from(body.matchAll(/{{\s*([^}]+?)\s*}}/g), m => m[1])))
}


export function extractedBodyReferences(actions: ScriptActionDefinition[]): string[] {
  const names = new Set<string>()
  const visit = (action: ScriptActionDefinition, depth: number) => {
    if (depth > 32) return
    for (const extractor of action.extractors || []) if (extractor.name) names.add(`var.${extractor.name}`)
    const control = action.control
    const requests = action.params?.requests
    const children = [...(control?.steps || []), ...(control?.else || []), ...(control?.default || []), ...(control?.cases || []).flatMap(branch => branch.steps || []), ...(control?.branches || []).flatMap(branch => branch.steps || []), ...(Array.isArray(requests) ? requests.filter(node => node && typeof node === 'object') as ScriptActionDefinition[] : [])]
    for (const child of children) visit(child, depth + 1)
  }
  for (const action of actions) visit(action, 0)
  return [...names]
}
