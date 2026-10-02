import { javaDependencyInventory } from './javaDependencyInventoryModel'

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

export function JavaDependencyInventory({ metadata }: { metadata: unknown }) {
  const inventory = javaDependencyInventory(metadata)
  if (!inventory) return null
  const counts = record(inventory.counts)
  const count = (key: string) => typeof counts?.[key] === 'number' && Number.isSafeInteger(counts[key]) && Number(counts[key]) >= 0 ? Number(counts[key]) : 0
  const files = Array.isArray(inventory.files) ? inventory.files.filter((value): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)).filter((file): file is Record<string, unknown> => Boolean(file && typeof file.source_path === 'string' && typeof file.role === 'string')) : []
  return <details className="lr-warnings lr-java-dependency-inventory">
    <summary>Java dependency inventory · {count('jars')} JARs · {count('classes')} compiled classes · {count('executables')} executable files</summary>
    <p>File paths, sizes, and hashes preserve source evidence. These files are not installed on runners and do not establish replay compatibility. Review the classpath, helper source, JDK, and runner policy before approval.</p>
    {files.length > 0 && <ul>{files.slice(0, 50).map((file, index) => <li key={`${file.source_path}:${index}`}>
      <code style={{ overflowWrap: 'anywhere', whiteSpace: 'normal' }}>{String(file.source_path)}</code> · {String(file.role)}
      {typeof file.size_bytes === 'number' && Number.isSafeInteger(file.size_bytes) && file.size_bytes >= 0 ? ` · ${file.size_bytes.toLocaleString()} bytes` : ''}
      {typeof file.sha256 === 'string' && /^[a-f0-9]{64}$/i.test(file.sha256) ? <> · SHA-256 <code style={{ overflowWrap: 'anywhere', whiteSpace: 'normal' }}>{file.sha256}</code></> : null}
    </li>)}</ul>}
    {files.length > 50 && <p>Showing 50 of {files.length} files. The complete inventory remains in the saved review evidence.</p>}
  </details>
}
