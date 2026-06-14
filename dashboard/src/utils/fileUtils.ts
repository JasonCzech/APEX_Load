export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

// downloadFilenameFromDisposition extracts the filename from a Content-Disposition
// response header, falling back to fallbackName when absent or unparseable.
export function downloadFilenameFromDisposition(disposition: string | null, fallbackName: string): string {
  const match = disposition?.match(/filename="?([^";]+)"?/i)
  return match?.[1] || fallbackName
}

export interface ZipFileEntry {
  name: string
  content: string | Uint8Array
  lastModified?: Date
}

const textEncoder = new TextEncoder()

let crc32Table: Uint32Array | null = null

function getCrc32Table(): Uint32Array {
  if (crc32Table) return crc32Table

  const table = new Uint32Array(256)
  for (let index = 0; index < 256; index += 1) {
    let value = index
    for (let bit = 0; bit < 8; bit += 1) {
      value = (value & 1) !== 0 ? 0xedb88320 ^ (value >>> 1) : value >>> 1
    }
    table[index] = value >>> 0
  }

  crc32Table = table
  return table
}

function crc32(bytes: Uint8Array): number {
  const table = getCrc32Table()
  let value = 0xffffffff
  for (const byte of bytes) {
    value = table[(value ^ byte) & 0xff] ^ (value >>> 8)
  }
  return (value ^ 0xffffffff) >>> 0
}

function encodeZipString(value: string): Uint8Array {
  return textEncoder.encode(value)
}

function asZipBytes(content: string | Uint8Array): Uint8Array {
  return typeof content === 'string' ? textEncoder.encode(content) : content
}

function createArrayBuffer(size: number): { buffer: ArrayBuffer; view: DataView; bytes: Uint8Array } {
  const buffer = new ArrayBuffer(size)
  return {
    buffer,
    view: new DataView(buffer),
    bytes: new Uint8Array(buffer),
  }
}

function writeDosDateTime(date: Date): { dosDate: number; dosTime: number } {
  const year = Math.min(Math.max(date.getFullYear(), 1980), 2107)
  return {
    dosDate: ((year - 1980) << 9) | ((date.getMonth() + 1) << 5) | date.getDate(),
    dosTime: (date.getHours() << 11) | (date.getMinutes() << 5) | Math.floor(date.getSeconds() / 2),
  }
}

function createLocalFileHeader(
  fileName: Uint8Array,
  payload: Uint8Array,
  checksum: number,
  lastModified: Date,
): Uint8Array {
  const { view, bytes } = createArrayBuffer(30 + fileName.length)
  const { dosDate, dosTime } = writeDosDateTime(lastModified)

  view.setUint32(0, 0x04034b50, true)
  view.setUint16(4, 20, true)
  view.setUint16(6, 0x0800, true)
  view.setUint16(8, 0, true)
  view.setUint16(10, dosTime, true)
  view.setUint16(12, dosDate, true)
  view.setUint32(14, checksum, true)
  view.setUint32(18, payload.length, true)
  view.setUint32(22, payload.length, true)
  view.setUint16(26, fileName.length, true)
  view.setUint16(28, 0, true)
  bytes.set(fileName, 30)

  return bytes
}

function createCentralDirectoryHeader(
  fileName: Uint8Array,
  payload: Uint8Array,
  checksum: number,
  lastModified: Date,
  localHeaderOffset: number,
): Uint8Array {
  const { view, bytes } = createArrayBuffer(46 + fileName.length)
  const { dosDate, dosTime } = writeDosDateTime(lastModified)

  view.setUint32(0, 0x02014b50, true)
  view.setUint16(4, 20, true)
  view.setUint16(6, 20, true)
  view.setUint16(8, 0x0800, true)
  view.setUint16(10, 0, true)
  view.setUint16(12, dosTime, true)
  view.setUint16(14, dosDate, true)
  view.setUint32(16, checksum, true)
  view.setUint32(20, payload.length, true)
  view.setUint32(24, payload.length, true)
  view.setUint16(28, fileName.length, true)
  view.setUint16(30, 0, true)
  view.setUint16(32, 0, true)
  view.setUint16(34, 0, true)
  view.setUint16(36, 0, true)
  view.setUint32(38, 0, true)
  view.setUint32(42, localHeaderOffset, true)
  bytes.set(fileName, 46)

  return bytes
}

function createEndOfCentralDirectory(entryCount: number, centralDirectorySize: number, centralDirectoryOffset: number): Uint8Array {
  const { view, bytes } = createArrayBuffer(22)
  view.setUint32(0, 0x06054b50, true)
  view.setUint16(4, 0, true)
  view.setUint16(6, 0, true)
  view.setUint16(8, entryCount, true)
  view.setUint16(10, entryCount, true)
  view.setUint32(12, centralDirectorySize, true)
  view.setUint32(16, centralDirectoryOffset, true)
  view.setUint16(20, 0, true)
  return bytes
}

export function createZipBlob(entries: ZipFileEntry[]): Blob {
  const localFileParts: Uint8Array[] = []
  const centralDirectoryParts: Uint8Array[] = []
  let offset = 0

  entries.forEach(entry => {
    const fileName = encodeZipString(entry.name)
    const payload = asZipBytes(entry.content)
    const checksum = crc32(payload)
    const lastModified = entry.lastModified || new Date()
    const localHeader = createLocalFileHeader(fileName, payload, checksum, lastModified)
    const centralHeader = createCentralDirectoryHeader(fileName, payload, checksum, lastModified, offset)

    localFileParts.push(localHeader, payload)
    centralDirectoryParts.push(centralHeader)

    offset += localHeader.length + payload.length
  })

  const centralDirectoryOffset = offset
  const centralDirectorySize = centralDirectoryParts.reduce((total, part) => total + part.length, 0)
  const endOfCentralDirectory = createEndOfCentralDirectory(entries.length, centralDirectorySize, centralDirectoryOffset)
  const blobParts = [...localFileParts, ...centralDirectoryParts, endOfCentralDirectory].map(part => {
    const copy = new Uint8Array(part.byteLength)
    copy.set(part)
    return copy
  })

  return new Blob(blobParts, { type: 'application/zip' })
}
