export type NativeImportTarget = {
  launcher: string
  mode?: string | null
}

export type NativeImportRequest = NativeImportTarget & {
  id: number
  files: File[]
}

export function nativeImportTargetForFileName(fileName: string): NativeImportTarget {
  const name = fileName.toLowerCase()
  if (name.endsWith('.bruno.zip') || name.endsWith('.bruno.tar') || name.endsWith('.bruno.tar.gz') || name.endsWith('.bru')) return { launcher: 'bruno' }
  if (name.endsWith('.zip')) return { launcher: 'enterprise_archive' }
  if (name.endsWith('.jmx')) return { launcher: 'jmeter' }
  if (name.endsWith('.har')) return { launcher: 'traffic_har', mode: 'auto_detect' }
  if (name.endsWith('.postman_collection.json') || name.endsWith('.postman_environment.json')) return { launcher: 'postman' }
  if (name.endsWith('.asyncapi.json') || name.endsWith('.asyncapi.yaml') || name.endsWith('.asyncapi.yml')) return { launcher: 'asyncapi' }
  if (name.endsWith('.openapi.json') || name.endsWith('.openapi.yaml') || name.endsWith('.openapi.yml')) return { launcher: 'openapi' }
  if (name.endsWith('.wsdl') || name.endsWith('.xml')) return { launcher: 'wsdl' }
  if (name.endsWith('.raml')) return { launcher: 'raml' }
  if (name.endsWith('.yaml') || name.endsWith('.yml')) return { launcher: 'yaml_chooser' }
  if (name.endsWith('.avro')) return { launcher: 'eventhubs_capture' }
  if (name.endsWith('.mqsc') || name.endsWith('.properties')) return { launcher: 'broker_config' }
  if (name.endsWith('.js') || name.endsWith('.ts')) return { launcher: 'k6' }
  if (name.endsWith('.http') || name.endsWith('.curl') || name.endsWith('.sh') || name.endsWith('.txt')) return { launcher: 'sample', mode: 'standard_api' }
  return { launcher: 'openapi' }
}

function hasBrunoSourceMarkers(content: string): boolean {
  const aliases = ['http', 'graphql', 'graphql-request', 'grpc', 'grpc-request', 'ws', 'ws-request', 'websocket', 'websocket-request', 'app', 'bruno-app']
  if (/^\s*opencollection\s*:/im.test(content)) return true
  if (/^\s*info\s*:/im.test(content) &&
      /^\s*type\s*:\s*(http|graphql(?:-request)?|grpc(?:-request)?|ws(?:-request)?|websocket(?:-request)?|app|bruno-app)\s*$/im.test(content)) {
    return true
  }
  try {
    const parsed = JSON.parse(content) as { info?: { type?: unknown }; opencollection?: unknown }
    const itemType = typeof parsed.info?.type === 'string' ? parsed.info.type.toLowerCase() : ''
    return parsed.opencollection !== undefined || aliases.includes(itemType)
  } catch {
    return false
  }
}

function hasOpenAPISourceMarkers(content: string): boolean {
  if (/^\s*(openapi|swagger)\s*:/im.test(content) || /^\s*paths\s*:/im.test(content)) return true
  try {
    const parsed = JSON.parse(content) as { openapi?: unknown; swagger?: unknown; paths?: unknown }
    return parsed.openapi !== undefined || parsed.swagger !== undefined || parsed.paths !== undefined
  } catch {
    return false
  }
}

export async function nativeImportTargetForFile(file: File): Promise<NativeImportTarget> {
  const name = file.name.toLowerCase()
  const namedTarget = nativeImportTargetForFileName(name)
  const inspectContent = (name.endsWith('.yaml') || name.endsWith('.yml') || name.endsWith('.json')) &&
    !name.endsWith('.postman_collection.json') &&
    !name.endsWith('.postman_environment.json') &&
    !name.endsWith('.asyncapi.json') &&
    !name.endsWith('.openapi.json')
  if (!inspectContent) return namedTarget
  const content = await file.slice(0, 128 * 1024).text()
  if (hasBrunoSourceMarkers(content)) return { launcher: 'bruno' }
  if (hasOpenAPISourceMarkers(content)) return { launcher: 'openapi' }
  return name.endsWith('.yaml') || name.endsWith('.yml') ? { launcher: 'yaml_chooser' } : namedTarget
}

export async function resolveNativeImportRequest(
  files: File[],
  requestID: number,
  isCurrent: (requestID: number) => boolean,
): Promise<NativeImportRequest | null> {
  const usable = files.filter(Boolean)
  if (usable.length === 0) return null
  let target: NativeImportTarget
  try {
    target = await nativeImportTargetForFile(usable[0])
  } catch {
    target = nativeImportTargetForFileName(usable[0].name)
  }
  if (!isCurrent(requestID)) return null
  return { id: requestID, files: usable, ...target }
}
