# BBC Random Links LoadRunner QC Script

This Web HTTP/HTML script opens `https://www.bbc.com/`, then follows five parameterized BBC links.

Each `BBCLink01` through `BBCLink05` parameter is table-backed from `BBCLinks.dat` with random row selection. The link pool is seeded from BBC homepage section/navigation links and is intended for importer/conversion QC.

Zip for upload:

```bash
cd examples/loadrunner
zip -r bbc-random-links.zip bbc-random-links
```
