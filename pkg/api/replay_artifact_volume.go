package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

const replayArtifactVolumeMarker = "source.v1"

// The marker identifies a physical archive namespace across process restarts.
// Paths alone cannot distinguish separate disks mounted at the same HA path.
// Publish the fully synced marker with an exclusive hard link, so concurrent
// openers never adopt a partially written identity or overwrite another source.
func replayArtifactVolumeIdentity(ctx context.Context, root *os.Root) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	namespace, err := openArchiveChildDirectoryAt(root, archiveReplayDirectory, true, 0700)
	if err != nil {
		return "", err
	}
	defer namespace.Close()

	identity, err := readReplayArtifactVolumeMarker(namespace)
	if os.IsNotExist(err) {
		var entropy [32]byte
		if _, err = rand.Read(entropy[:]); err != nil {
			return "", err
		}
		candidate := hex.EncodeToString(entropy[:])
		temporary := "source-" + candidate + ".tmp"
		file, err := namespace.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		defer namespace.Remove(temporary)
		_, writeErr := io.WriteString(file, candidate)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if err = errors.Join(writeErr, file.Close()); err != nil {
			return "", err
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
		if err = namespace.Link(temporary, replayArtifactVolumeMarker); err != nil && !os.IsExist(err) {
			return "", err
		}
		identity, err = readReplayArtifactVolumeMarker(namespace)
	}
	if err != nil {
		return "", err
	}
	// Every successful opener syncs the directory entries too, including one
	// racing with the original creator between link and directory sync.
	if err = syncOpenedArchiveDirectory(namespace); err != nil {
		return "", err
	}
	if err = syncOpenedArchiveDirectory(root); err != nil {
		return "", err
	}
	return identity, ctx.Err()
}

// Sync even an already-absent generation on retry: an earlier unlink may have
// returned before its directory sync completed. Only the held source is touched.
func (b *replayArtifactBackend) syncLocalReplayParents() error {
	namespace, err := openArchiveChildDirectoryAt(b.localRoot, archiveReplayDirectory, false, 0)
	if os.IsNotExist(err) {
		return syncOpenedArchiveDirectory(b.localRoot)
	}
	if err != nil {
		return err
	}
	defer namespace.Close()
	versions, err := openArchiveChildDirectoryAt(namespace, "v1", false, 0)
	if os.IsNotExist(err) {
		return errors.Join(syncOpenedArchiveDirectory(namespace), syncOpenedArchiveDirectory(b.localRoot))
	}
	if err != nil {
		return err
	}
	defer versions.Close()
	return errors.Join(syncOpenedArchiveDirectory(versions), syncOpenedArchiveDirectory(namespace), syncOpenedArchiveDirectory(b.localRoot))
}

func readReplayArtifactVolumeMarker(namespace *os.Root) (string, error) {
	file, _, err := openArchiveArtifactForReadAt(namespace, replayArtifactVolumeMarker)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 65))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	if !replayArtifactHex(string(data)) {
		return "", errors.New("invalid replay archive volume identity")
	}
	return string(data), nil
}

func (b *replayArtifactBackend) verifyLocalReplayIdentity() error {
	namespace, err := openArchiveChildDirectoryAt(b.localRoot, archiveReplayDirectory, false, 0)
	if err != nil {
		return err
	}
	defer namespace.Close()
	identity, err := readReplayArtifactVolumeMarker(namespace)
	if err != nil {
		return err
	}
	if identity != b.volumeIdentity {
		return errors.New("replay archive volume identity changed")
	}
	return nil
}
