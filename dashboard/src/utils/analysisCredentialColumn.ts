// Analytical label admission mirrors pkg/credential.SensitiveName after the
// caller's finite UTF-8/path checks. Exact normalized components are used; an
// incidental substring such as tokenization or monkey is not protected.
const protectedNames = new Set([
  'accesskey', 'accesskeyid', 'accesstoken', 'accountkey', 'acktoken', 'apikey', 'apitoken',
  'auth', 'authentication', 'authorization', 'basicauth',
  'clientsecret', 'connectionstring', 'cookie', 'credential', 'credentials', 'csrf',
  'csrftoken', 'deliverytag', 'dsn', 'idsecret', 'idtoken',
  'jmssettlementtoken', 'locktoken', 'password', 'pass', 'passcode', 'passphrase', 'passwd',
  'privatekey', 'proxyauthorization', 'pwd', 'receipthandle',
  'refreshtoken', 'sas', 'saskey', 'sassignature', 'secret',
  'secretaccesskey', 'sharedaccesskey', 'sharedaccesssignature',
  'session', 'sessionid', 'sessiontoken', 'sid', 'storagestate',
  'settlementtoken', 'signature', 'token', 'xsrf', 'xsrftoken',
])

// Mirrors pkg/credential credentialPropertyWords (no URL words).
const credentialPropertyWords = new Set([
  'count', 'duration', 'enabled', 'expiration', 'expires', 'expiry',
  'feature', 'features', 'flag', 'flags', 'length', 'method', 'mode',
  'timeout', 'ttl', 'type', 'version',
])
const letterOrDigit = /^[\p{L}\p{Nd}]$/u
function normalize(value: string): string {
  let output = ''
  for (const char of decodedName(value)) if (letterOrDigit.test(char)) output += char === "\u0130" ? "i" : char.toLowerCase()
  return output
}
function goWhitespace(char: string): boolean {
  const point = char.codePointAt(0)!
  return point >= 0x09 && point <= 0x0d || point === 0x20 || point === 0x85 || point === 0xa0 || point === 0x1680 || point >= 0x2000 && point <= 0x200a || point === 0x2028 || point === 0x2029 || point === 0x202f || point === 0x205f || point === 0x3000
}
function trimGoWhitespace(value: string): string {
  const characters = Array.from(value)
  let first = 0, last = characters.length
  while (first < last && goWhitespace(characters[first])) first++
  while (last > first && goWhitespace(characters[last - 1])) last--
  return characters.slice(first, last).join('')
}
function unescapeQuery(value: string): string | undefined {
  const bytes = new TextEncoder().encode(value)
  const output = new Uint8Array(bytes.length)
  let at = 0
  for (let index = 0; index < bytes.length; index++) {
    if (bytes[index] === 0x25) {
      const digit = (byte: number) => byte >= 0x30 && byte <= 0x39 ? byte - 0x30 : byte >= 0x41 && byte <= 0x46 ? byte - 0x41 + 10 : byte >= 0x61 && byte <= 0x66 ? byte - 0x61 + 10 : -1
      if (index + 2 >= bytes.length) return
      const high = digit(bytes[index + 1]), low = digit(bytes[index + 2])
      if (high < 0 || low < 0) return
      output[at++] = high * 16 + low
      index += 2
    } else output[at++] = bytes[index] === 0x2b ? 0x20 : bytes[index]
  }
  // QueryUnescape accepts arbitrary decoded bytes. Replacement runes are
  // separators for the protected ASCII vocabulary, matching Go iteration.
  return new TextDecoder('utf-8', { ignoreBOM: true }).decode(output.subarray(0, at))
}
function decodedName(value: string): string {
  let current = trimGoWhitespace(value)
  for (let pass = 0; pass < 3; pass++) {
    const next = unescapeQuery(current)
    if (next === undefined || next === current) break
    current = next
  }
  return trimGoWhitespace(current)
}

export function analysisProtectedIdentityColumn(path: string[]): boolean {
  // Defensive finite admission precedes normalization/decoding/joining even
  // when this helper is called outside the ordinary admitted mapping path.
  if (!Array.isArray(path) || Object.getPrototypeOf(path) !== Array.prototype) return true
  const count = Object.getOwnPropertyDescriptor(path, 'length')?.value
  if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 1 || count > 16) return true
  const admitted: string[] = []
  for (let component = 0; component < count; component++) {
    const descriptor = Object.getOwnPropertyDescriptor(path, String(component))
    if (!descriptor || !Object.prototype.hasOwnProperty.call(descriptor, 'value')) return true
    const part = descriptor.value
    if (typeof part !== 'string' || part.length < 1 || part.length > 256 || part.includes('\0')) return true
    let bytes = 0
    for (let index = 0; index < part.length; index++) {
      const point = part.charCodeAt(index)
      if (point >= 0xd800 && point <= 0xdbff) {
        const low = part.charCodeAt(++index)
        if (!(low >= 0xdc00 && low <= 0xdfff)) return true
        bytes += 4
      } else if (point >= 0xdc00 && point <= 0xdfff) return true
      else bytes += point <= 0x7f ? 1 : point <= 0x7ff ? 2 : 3
      if (bytes > 256) return true
    }
    admitted.push(part)
  }
  const sensitive = (name: string) => {
    name = decodedName(name)
    if (protectedNames.has(normalize(name))) return true
    const parts = name.split(/[^\p{L}\p{Nd}]+/u).filter(Boolean)
    // A final property word describes the credential (session_count).
    const describesCredential = parts.length > 1 && credentialPropertyWords.has(normalize(parts[parts.length - 1]))
    for (let start = 0; start < parts.length; start++) {
      let combined = ''
      for (let end = start; end < parts.length && end < start + 4; end++) {
        combined += normalize(parts[end])
        if (protectedNames.has(combined) && !(describesCredential && end < parts.length - 1)) return true
      }
    }
    return false
  }
  return admitted.some(sensitive) || sensitive(admitted.join('.'))
}
