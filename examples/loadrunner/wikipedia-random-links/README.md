# Wikipedia Random Links LoadRunner QC Script

This Web HTTP/HTML script opens `https://www.wikipedia.org/`, then follows five parameterized Wikipedia links.

Each `WikiLink01` through `WikiLink05` parameter is table-backed from `WikipediaLinks.dat` with random row selection. The link pool is seeded from visible links on the Wikipedia portal page and is intended for importer/conversion QC.

Zip for upload:

```bash
cd examples/loadrunner
zip -r wikipedia-random-links.zip wikipedia-random-links
```
