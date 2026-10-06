# Committed frontend bundle baseline

This diagnostic builds committed `9b5b9e29a48950591f9e06d7acb7d2d3095cae4b` from a create-only private snapshot, using the existing dependency installation, Node 22.22.0, deterministic 1970 timestamps, production Vite manifest output and unchanged committed budget checker. It does not qualify the current feature composition or change any limit.

The first snapshot omitted the committed `pkg/api/import_settings_fields.json` input. That build failed before sizing; the original raw output and receipt are retained. The corrected snapshot adds only that exact Git object. All 1,522 final input hashes stayed unchanged through the successful Vite build. Source manifests include exact committed dashboard and referenced documentation/MCP/deployment inputs. Build outputs remain private; the receipt records their hashes.

The successful baseline has 185 JavaScript assets (7,268,947 raw bytes; 2,023,504 gzip bytes) and 58 CSS assets (909,795 raw bytes; 162,916 gzip bytes). Shell, Scripts and Test Execution static JavaScript total 191,082, 250,161 and 447,064 gzip bytes respectively. The unchanged checker fails the same nine categories reported by the current composition. This establishes historical budget drift, not acceptable startup performance.

`bundle-result-v2.json` retains every asset and recursive route closure. `comparison-calibration-review.json` compares exact baseline data with the parent's explicitly rounded current report; it does not claim exact current byte equality. It reviews the proposed aggregate-only calibration conditionally on final candidate measurement, while keeping all per-asset, route and forbidden-asset limits unchanged. Loading and global CSS violations still require fixes and a final green deterministic budget gate.

Raw logs use deterministic gzip; the index records their original decompressed SHA-256. No source text or failed evidence was trimmed. No installation, primary-checkout write, production-source edit, budget edit or shared cache cleanup occurred in this diagnostic.
