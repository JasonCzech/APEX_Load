package api

import "sync"

// archiveReadCaches belongs to one controller and is shared with its runner so
// lifecycle invalidation reaches every reader. Separate in-process controllers
// never reuse each other's catalog identities, decoded reports or export plans.
type archiveReadCaches struct {
	catalogIdentities *boundedArchiveCache[archiveCatalogIdentity]
	reports           *boundedArchiveCache[RunArchiveReport]
	manifests         *boundedArchiveCache[RunArchiveManifest]
	sanitizedPlans    *boundedArchiveCache[sanitizedRunArchivePlan]
	catalogReadSlots  chan struct{}
}

func newArchiveReadCaches() *archiveReadCaches {
	return &archiveReadCaches{
		catalogIdentities: newBoundedArchiveCache[archiveCatalogIdentity](128),
		reports:           newWeightedBoundedArchiveCache(runArchiveReportCacheEntries, runArchiveReportCacheBytes, archiveJSONWeight[RunArchiveReport]),
		manifests:         newWeightedBoundedArchiveCache(runArchiveManifestCacheEntries, runArchiveManifestCacheBytes, archiveJSONWeight[RunArchiveManifest]),
		sanitizedPlans:    newWeightedBoundedArchiveCache(runArchiveManifestCacheEntries, runArchiveManifestCacheBytes, sanitizedArchivePlanWeight),
		catalogReadSlots:  make(chan struct{}, 2),
	}
}

type archiveCacheOwner struct {
	once   sync.Once
	caches *archiveReadCaches
}

func (o *archiveCacheOwner) get(create func() *archiveReadCaches) *archiveReadCaches {
	o.once.Do(func() {
		if o.caches == nil {
			o.caches = create()
		}
	})
	return o.caches
}

func (tr *TestRunner) archiveReadCaches() *archiveReadCaches {
	return tr.archiveCacheOwner.get(newArchiveReadCaches)
}

func (s *Server) archiveReadCaches() *archiveReadCaches {
	return s.archiveCacheOwner.get(func() *archiveReadCaches {
		if s.runner != nil {
			return s.runner.archiveReadCaches()
		}
		return newArchiveReadCaches()
	})
}
