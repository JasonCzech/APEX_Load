package storage

import (
	"path/filepath"
	"testing"
)

func TestImportAssetLifecycleUsesRevisionCAS(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "assets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset := &ImportAsset{ID: "asset-1", ProjectID: DefaultProjectID, ImportID: "import-1", ObjectKey: "objects/asset-1", SHA256: "abc", SizeBytes: 3, State: ImportAssetUploading}
	if err := store.CreateImportAsset(asset); err != nil {
		t.Fatal(err)
	}
	expected := asset.Revision
	asset.Revision++
	asset.State = ImportAssetReady
	changed, err := store.TransitionImportAsset(asset, expected)
	if err != nil || !changed {
		t.Fatalf("TransitionImportAsset = (%v, %v)", changed, err)
	}
	asset.State = ImportAssetFailed
	if changed, err := store.TransitionImportAsset(asset, expected); err != nil || changed {
		t.Fatalf("stale TransitionImportAsset = (%v, %v), want refusal", changed, err)
	}
	got, err := store.GetImportAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ImportAssetReady || got.ObjectKey != asset.ObjectKey {
		t.Fatalf("asset = %+v", got)
	}
}
