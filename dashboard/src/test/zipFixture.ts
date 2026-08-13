const ZIP_UTF8_FLAG = 0x0800

function concatBuffers(buffers: ArrayBuffer[]): ArrayBuffer {
  const result = new Uint8Array(buffers.reduce((total, buffer) => total + buffer.byteLength, 0))
  let offset = 0
  for (const buffer of buffers) {
    result.set(new Uint8Array(buffer), offset)
    offset += buffer.byteLength
  }
  return result.buffer
}

// Produces a complete, standards-shaped ZIP whose members are empty and stored
// without compression. Import-routing tests only need real local headers,
// central-directory records, and an EOCD record; zero-byte members keep the
// fixture deterministic without adding a compression dependency to Vitest.
export function storedZipFixture(memberNames: string[]): ArrayBuffer {
  if (memberNames.length > 0xffff) throw new Error('ZIP fixture exceeds the non-ZIP64 entry limit')
  const encoder = new TextEncoder()
  const localRecords: ArrayBuffer[] = []
  const centralRecords: ArrayBuffer[] = []
  let localOffset = 0

  for (const memberName of memberNames) {
    const name = encoder.encode(memberName)
    const local = new ArrayBuffer(30 + name.length)
    const localView = new DataView(local)
    localView.setUint32(0, 0x04034b50, true)
    localView.setUint16(4, 20, true)
    localView.setUint16(6, ZIP_UTF8_FLAG, true)
    localView.setUint16(26, name.length, true)
    new Uint8Array(local).set(name, 30)
    localRecords.push(local)

    const central = new ArrayBuffer(46 + name.length)
    const centralView = new DataView(central)
    centralView.setUint32(0, 0x02014b50, true)
    centralView.setUint16(4, 20, true)
    centralView.setUint16(6, 20, true)
    centralView.setUint16(8, ZIP_UTF8_FLAG, true)
    centralView.setUint16(28, name.length, true)
    centralView.setUint32(42, localOffset, true)
    new Uint8Array(central).set(name, 46)
    centralRecords.push(central)
    localOffset += local.byteLength
  }

  const centralDirectory = concatBuffers(centralRecords)
  const end = new ArrayBuffer(22)
  const endView = new DataView(end)
  endView.setUint32(0, 0x06054b50, true)
  endView.setUint16(8, memberNames.length, true)
  endView.setUint16(10, memberNames.length, true)
  endView.setUint32(12, centralDirectory.byteLength, true)
  endView.setUint32(16, localOffset, true)
  return concatBuffers([...localRecords, centralDirectory, end])
}
