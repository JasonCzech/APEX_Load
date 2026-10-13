import spec from './catalogActionFormFixture.json'
import type { DslCatalogAction } from './dslActionCatalog'

/**
 * The live_table catalog entry exactly as the controller serves it, for form
 * tests; pkg/dsl TestLiveTableCatalogFixtureIsFresh keeps the JSON current.
 */
export const LIVE_TABLE_SPEC = spec as DslCatalogAction
