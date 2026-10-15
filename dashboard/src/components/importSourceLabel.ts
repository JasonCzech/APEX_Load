/**
 * The readable name of an import's source file, the default for the scenario
 * and the folder an import makes: "checkout_plan.jmx" → "checkout plan",
 * "Orders.postman_collection.json" → "Orders". (Bruno's collection may be a
 * folder, whose dots are not extensions, so it names itself.)
 */
export function importSourceLabel(sourceName: string | undefined): string {
  const base = (sourceName || '').split(/[\\/]/).filter(Boolean).pop() || ''
  return base
    .replace(/\.(?:tar\.gz|tgz)$/i, '')
    .replace(/\.[^.]+$/, '')
    // The suffix an exporter adds before the extension (Orders.postman_collection.json).
    .replace(/\.(?:postman_collection|postman_environment|openapi|swagger|asyncapi|bruno|har)$/i, '')
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}
