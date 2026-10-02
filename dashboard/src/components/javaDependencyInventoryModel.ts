function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

export function javaDependencyInventory(metadata: unknown): Record<string, unknown> | null {
  const inventory = record(record(metadata)?.java_dependency_inventory)
  return inventory?.contract === 'apexload.java-dependency-inventory.v1' ? inventory : null
}
