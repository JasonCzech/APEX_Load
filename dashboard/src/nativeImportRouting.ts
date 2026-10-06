import { importFileLogicalPath, importLogicalBase } from './importLogicalPath'

export type NativeImportTarget = {
  launcher: string
  mode?: string | null
}

export type NativeImportRequest = NativeImportTarget & {
  id: number
  files: File[]
}

const ZIP_END_OF_DIRECTORY_BYTES = 22
const ZIP_MAX_COMMENT_BYTES = 0xffff
const ZIP_CENTRAL_DIRECTORY_HEADER_BYTES = 46
const ZIP_CENTRAL_DIRECTORY_READ_BYTES = 256 * 1024
const ZIP_END_OF_DIRECTORY_SIGNATURE = 0x06054b50
const ZIP_CENTRAL_DIRECTORY_SIGNATURE = 0x02014b50

function isZipArchiveFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.zip')
}

// visitZipMemberNames walks the member names of a ZIP's central directory with
// bounded reads, calling visit for each until it returns true. It resolves true
// when every member was visited or the visitor stopped the walk, and false when
// the archive cannot be read.
async function visitZipMemberNames(file: File, visit: (memberName: string) => boolean): Promise<boolean> {
  if (file.size === 0) return false
  try {
    // The end record gives the exact central-directory range. Reading from that
    // offset avoids false negatives when a large directory pushes an early
    // OpenCollection entry outside a fixed tail window, while every browser
    // allocation remains bounded to a header or member name.
    const tailStart = Math.max(0, file.size - ZIP_END_OF_DIRECTORY_BYTES - ZIP_MAX_COMMENT_BYTES)
    const tail = new Uint8Array(await file.slice(tailStart).arrayBuffer())
    let centralDirectoryOffset = -1
    let centralDirectorySize = -1
    let totalEntries = -1
    for (let offset = tail.length - ZIP_END_OF_DIRECTORY_BYTES; offset >= 0; offset -= 1) {
      const view = new DataView(tail.buffer, tail.byteOffset + offset, ZIP_END_OF_DIRECTORY_BYTES)
      if (view.getUint32(0, true) !== ZIP_END_OF_DIRECTORY_SIGNATURE) continue
      const diskNumber = view.getUint16(4, true)
      const centralDirectoryDisk = view.getUint16(6, true)
      const entriesOnDisk = view.getUint16(8, true)
      const entries = view.getUint16(10, true)
      const directorySize = view.getUint32(12, true)
      const directoryOffset = view.getUint32(16, true)
      const commentLength = view.getUint16(20, true)
      const absoluteEndOffset = tailStart + offset
      if (absoluteEndOffset + ZIP_END_OF_DIRECTORY_BYTES + commentLength !== file.size ||
        diskNumber !== 0 || centralDirectoryDisk !== 0 || entriesOnDisk !== entries ||
        entries === 0xffff || directorySize === 0xffffffff || directoryOffset === 0xffffffff ||
        directoryOffset + directorySize > absoluteEndOffset) continue
      centralDirectoryOffset = directoryOffset
      centralDirectorySize = directorySize
      totalEntries = entries
      break
    }
    if (centralDirectoryOffset < 0 || centralDirectorySize < 0 || totalEntries < 0) return false

    const decoder = new TextDecoder('utf-8')
    const directoryEnd = centralDirectoryOffset + centralDirectorySize
    let chunkStart = -1
    let chunk = new Uint8Array()
    const readDirectoryRange = async (offset: number, length: number) => {
      if (offset < centralDirectoryOffset || offset + length > directoryEnd) return null
      if (offset < chunkStart || offset + length > chunkStart + chunk.length) {
        chunkStart = offset
        chunk = new Uint8Array(await file.slice(
          offset,
          Math.min(directoryEnd, offset + Math.max(length, ZIP_CENTRAL_DIRECTORY_READ_BYTES)),
        ).arrayBuffer())
      }
      const relativeOffset = offset - chunkStart
      return chunk.subarray(relativeOffset, relativeOffset + length)
    }
    let entryOffset = centralDirectoryOffset
    for (let entryIndex = 0; entryIndex < totalEntries; entryIndex += 1) {
      if (entryOffset + ZIP_CENTRAL_DIRECTORY_HEADER_BYTES > directoryEnd) return false
      const header = await readDirectoryRange(entryOffset, ZIP_CENTRAL_DIRECTORY_HEADER_BYTES)
      if (!header) return false
      const view = new DataView(header.buffer, header.byteOffset, header.byteLength)
      if (view.getUint32(0, true) !== ZIP_CENTRAL_DIRECTORY_SIGNATURE) return false
      const nameLength = view.getUint16(28, true)
      const extraLength = view.getUint16(30, true)
      const commentLength = view.getUint16(32, true)
      const nameStart = entryOffset + ZIP_CENTRAL_DIRECTORY_HEADER_BYTES
      const nameEnd = nameStart + nameLength
      const nextEntryOffset = nameEnd + extraLength + commentLength
      if (nextEntryOffset > directoryEnd) return false
      const name = await readDirectoryRange(nameStart, nameLength)
      if (!name) return false
      if (visit(decoder.decode(name).replace(/\\/g, '/'))) return true
      entryOffset = nextEntryOffset
    }
    return true
  } catch {
    return false
  }
}

function zipMemberBase(memberName: string): string {
  return (memberName.split('/').pop() || '').toLowerCase()
}

export async function archiveContainsOpenCollectionMarker(file: File): Promise<boolean> {
  if (!isZipArchiveFile(file)) return false
  let found = false
  await visitZipMemberNames(file, memberName => {
    found = /^opencollection\.ya?ml$/.test(zipMemberBase(memberName))
    return found
  })
  return found
}

// archiveIsJMeterProject recognizes a ZIP or LoadRunner Enterprise USZ whose
// only scripts are JMeter plans: a .jmx and no VuGen C, Java or JavaScript
// sources. Those archives belong to the JMeter importer, which reads the plan
// inside directly; the enterprise archive importer can only review them.
export async function archiveIsJMeterProject(file: File): Promise<boolean> {
  const name = file.name.toLowerCase()
  if (!name.endsWith('.zip') && !name.endsWith('.usz')) return false
  let plans = false
  let sources = false
  const complete = await visitZipMemberNames(file, memberName => {
    const base = zipMemberBase(memberName)
    if (base.endsWith('.jmx')) plans = true
    sources = /\.(?:c|h|java|js|ts)$/.test(base)
    return sources
  })
  return complete && plans && !sources
}

export function nativeImportTargetForFileName(fileName: string): NativeImportTarget {
  const name = fileName.toLowerCase()
  if (name.endsWith('.bruno.zip') || name.endsWith('.bruno.tar') || name.endsWith('.bruno.tar.gz') || name.endsWith('.bru')) return { launcher: 'bruno' }
  if (name.endsWith('.zip') || name.endsWith('.usz')) return { launcher: 'enterprise_archive' }
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

export function hasBrunoSourceMarkers(content: string): boolean {
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

export function isOpenCollectionFileName(file: File): boolean {
  try {
    return /^opencollection\.ya?ml$/i.test(importLogicalBase(importFileLogicalPath(file)))
  } catch {
    return false
  }
}

export async function filesContainNativeBrunoSource(files: File[]): Promise<boolean> {
  const usable = files.filter(Boolean)
  if (usable.some(isOpenCollectionFileName)) return true
  const matches = await Promise.all(usable.map(async file => {
    const name = file.name.toLowerCase()
    if (name.endsWith('.zip')) return archiveContainsOpenCollectionMarker(file)
    if (!name.endsWith('.yaml') && !name.endsWith('.yml') && !name.endsWith('.json')) return false
    try {
      return hasBrunoSourceMarkers(await file.slice(0, 128 * 1024).text())
    } catch {
      return false
    }
  }))
  return matches.some(Boolean)
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

// Chrome DevTools Recorder exports are a `steps` array whose entries carry a
// recorder step `type` and ranked `selectors` candidates. Matched with regexes
// so a recording larger than the 128 KiB sniff window still classifies.
function hasRecorderSourceMarkers(content: string): boolean {
  if (!/"steps"\s*:\s*\[/.test(content)) return false
  if (/"selectors"\s*:\s*\[/.test(content)) return true
  return /"type"\s*:\s*"(?:setViewport|emulateNetworkConditions|navigate|click|doubleClick|hover|change|keyDown|keyUp|waitForElement|waitForExpression|scroll|close|customStep)"/.test(content)
}

function scriptImportTargetFromContent(content: string): NativeImportTarget | null {
  if (/\bload\.(?:WebRequest|initialize|action|sleep|Transaction)\b/.test(content)) {
    return { launcher: 'devweb' }
  }
  if (/from\s+['"]@playwright\/test['"]|require\(['"]@playwright\/test['"]\)|\bpage\.(?:goto|click|fill|locator)\s*\(/.test(content)) {
    return { launcher: 'recorder_export' }
  }
  if (/from\s+['"]k6(?:\/http)?['"]|require\(['"]k6(?:\/http)?['"]\)|\bhttp\.(?:get|post|put|patch|del|request)\s*\(/.test(content)) {
    return { launcher: 'k6' }
  }
  return null
}

export async function nativeImportTargetForFile(file: File): Promise<NativeImportTarget> {
  const name = file.name.toLowerCase()
  if (isOpenCollectionFileName(file)) return { launcher: 'bruno', mode: 'native_requests' }
  if (name.endsWith('.zip') && await archiveContainsOpenCollectionMarker(file)) {
    return { launcher: 'bruno', mode: 'native_requests' }
  }
  if (await archiveIsJMeterProject(file)) return { launcher: 'jmeter' }
  const namedTarget = nativeImportTargetForFileName(name)
  if (name.endsWith('.js') || name.endsWith('.ts')) {
    const content = await file.slice(0, 128 * 1024).text()
    return scriptImportTargetFromContent(content) || namedTarget
  }
  const inspectContent = (name.endsWith('.yaml') || name.endsWith('.yml') || name.endsWith('.json')) &&
    !name.endsWith('.postman_collection.json') &&
    !name.endsWith('.postman_environment.json') &&
    !name.endsWith('.asyncapi.json') &&
    !name.endsWith('.openapi.json')
  if (!inspectContent) return namedTarget
  const content = await file.slice(0, 128 * 1024).text()
  if (hasBrunoSourceMarkers(content)) return { launcher: 'bruno', mode: 'native_requests' }
  if (hasRecorderSourceMarkers(content)) return { launcher: 'recorder_export' }
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
  const targets = await Promise.all(usable.map(async file => {
    try {
      return await nativeImportTargetForFile(file)
    } catch {
      return nativeImportTargetForFileName(file.name)
    }
  }))
  if (!isCurrent(requestID)) return null
  const target = targets.find(candidate => candidate.launcher === 'bruno' && candidate.mode === 'native_requests')
    || targets.find(candidate => candidate.launcher === 'bruno')
    || targets[0]
  return { id: requestID, files: usable, ...target }
}
