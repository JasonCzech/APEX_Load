package api

// Test stores that override a few storage methods hold the real store in a
// named field (`SQLiteStore *storage.SQLiteStore` or `Store storage.Store`)
// instead of embedding it, and are handed to the server wrapped in an overlay:
// sqliteOverlay(fake) or storeOverlay(fake). Embedding made the compiler build
// a value and a pointer wrapper for every promoted method of every fake, about
// 200,000 functions; with them the package's test compile peaked at 8-9.5 GB
// and took three times as long. An overlay has exactly its base's method set, so
// the server's optional-interface assertions see what they saw before; each of
// its methods calls the fake's own method of that name and signature when it
// has one, else the store in the fake's field. Wrapping the same fake again
// returns the same overlay, so store identity checks (nativeSameStore) still
// see one store per fake. A fake that adds methods outside its base's method
// set (an optional interface over storage.Store, its own or embedded) still
// embeds its base.
//
//go:generate go run ../../cmd/storeoverlay -out store_overlay_test.go

// overlaidFake returns the fake inside an overlay, or store itself.
func overlaidFake(store any) any {
	if overlay, ok := store.(interface{ overlaidFake() any }); ok {
		return overlay.overlaidFake()
	}
	return store
}
