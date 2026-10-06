package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/keywrap"
)

// brunoSourceRetentionUnavailableCode is the machine-readable marker the import
// UI keys on to explain why Bruno import cannot start. It is a deployment
// configuration state, not a request error.
const brunoSourceRetentionUnavailableCode = "bruno_source_retention_unavailable"

// Key custody states reported by the support matrix. Bruno import always
// encrypts its retained source; these describe who holds the envelope key.
const (
	brunoKeyCustodyOperator = "operator_managed"
	brunoKeyCustodyLocal    = "local_auto"
	brunoKeyCustodyNone     = "unavailable"
)

// brunoSourceRetentionUnavailableMessage is deliberately client-visible. It
// contains no internal error text — only the operator action that resolves the
// condition — and it is the whole point of the response: without it the import
// UI can offer a complete, enabled workflow that dead-ends at inspection.
const brunoSourceRetentionUnavailableMessage = "Bruno import requires protected source retention, which needs an operator-managed envelope key in a production deployment. " +
	"Set " + keywrap.MasterKeyEnvVar + " (32 bytes, or base64-encoded 32 bytes) or configure " +
	keywrap.KeyVaultKeyIDEnvVar + ", then restart the controller."

// brunoLocalKeyAdvisory explains the degraded default. Import works with no
// configuration at all; the cost is key custody, not confidentiality of the
// stored bytes, and the advisory says so rather than implying managed custody.
const brunoLocalKeyAdvisory = "Bruno source is encrypted with a local key this controller generated for itself, stored beside the data. " +
	"Set " + keywrap.MasterKeyEnvVar + " or " + keywrap.KeyVaultKeyIDEnvVar + " for operator-managed key custody. " +
	"Sources imported under the local key cannot be read after switching keys."

const brunoLocalArtifactKeyFile = ".bruno-artifact-key"

var brunoLocalKeyWarnOnce sync.Once

// brunoSourceRetentionKeyCustody reports which envelope key the Bruno artifact
// store will use.
//
// Bruno is the one importer that cannot degrade by skipping protected
// retention: it publishes the encrypted raw and sanitized source bundles during
// inspection, and native commits preserve those sources for later review. Every other
// protected-import path treats retention as additive and falls back to the
// ordinary flow (see import_source_retention.go), so before this the Bruno
// importer alone hard-failed on an unconfigured deployment.
//
// The degraded default keeps the whole pipeline intact and only moves key
// custody: with no operator key configured, a non-production controller
// provisions a local key beside its own data. A production posture still
// demands an operator-managed key — silently self-issuing one there would
// misrepresent the retention guarantee the deployment was configured for.
func (s *Server) brunoSourceRetentionKeyCustody() string {
	if s == nil {
		return brunoKeyCustodyNone
	}
	s.brunoArtifactMu.Lock()
	resolved := s.brunoArtifactKeyCustody
	s.brunoArtifactMu.Unlock()
	if resolved != "" {
		// The store is open: report the key it was actually opened with, not
		// what a fresh resolution would pick now.
		return resolved
	}
	return s.brunoProspectiveKeyCustody()
}

// brunoProspectiveKeyCustody is the custody a fresh store would resolve. It
// takes no locks so it can run inside the artifact-store critical section.
func (s *Server) brunoProspectiveKeyCustody() string {
	if s == nil {
		return brunoKeyCustodyNone
	}
	if s.brunoArtifactWrapper != nil || s.desktopMode {
		return brunoKeyCustodyOperator
	}
	if strings.TrimSpace(os.Getenv(keywrap.MasterKeyEnvVar)) != "" ||
		strings.TrimSpace(os.Getenv(keywrap.KeyVaultKeyIDEnvVar)) != "" {
		return brunoKeyCustodyOperator
	}
	if productionMode() {
		return brunoKeyCustodyNone
	}
	return brunoKeyCustodyLocal
}

// brunoSourceRetentionAvailable reports whether Bruno import can retain its
// source at all. Only a production deployment with no configured key answers
// false.
func (s *Server) brunoSourceRetentionAvailable() bool {
	return s.brunoSourceRetentionKeyCustody() != brunoKeyCustodyNone
}

// brunoRetentionStorePresent reports whether Bruno's own artifact store can be
// opened — including under the local key. Use it for Bruno-owned maintenance
// (session pruning, project deletion); use brunoArtifactStorePresent for the
// shared protected-import facility, which stays on operator custody.
func (s *Server) brunoRetentionStorePresent() (bool, error) {
	if shared, err := s.brunoArtifactStorePresent(); err != nil || shared {
		return shared, err
	}
	return s.brunoSourceRetentionKeyCustody() == brunoKeyCustodyLocal, nil
}

// brunoArtifactKeyWrapper resolves the envelope key for the artifact store,
// falling back to the locally provisioned key when no operator key is
// configured and the posture permits it. It reports the custody it resolved so
// the caller can record what the open store is actually protected by.
//
// It must not take brunoArtifactMu: the store builder calls it while holding it.
func (s *Server) brunoArtifactKeyWrapper(root string) (keywrap.KeyWrapper, string, error) {
	wrapper, err := keywrap.NewConfiguredKeyWrapper(s.desktopMode)
	if err == nil {
		return wrapper, brunoKeyCustodyOperator, nil
	}
	if s.brunoProspectiveKeyCustody() != brunoKeyCustodyLocal {
		return nil, "", err
	}
	local, localErr := brunoLocalArtifactKeyWrapper(root)
	if localErr != nil {
		// Report the original configuration error: the local fallback is a
		// convenience, and its failure must not hide what is actually missing.
		return nil, "", errors.Join(err, localErr)
	}
	brunoLocalKeyWarnOnce.Do(func() {
		slog.Warn("Bruno source retention is using a locally provisioned envelope key",
			"key_file", brunoLocalArtifactKeyPath(root),
			"remedy", "set "+keywrap.MasterKeyEnvVar+" or "+keywrap.KeyVaultKeyIDEnvVar+" for operator-managed key custody")
	})
	return local, brunoKeyCustodyLocal, nil
}

// brunoLocalArtifactKeyPath keeps the key beside the store root rather than
// inside it, so the store's own sweeps (stale temporaries, expired sessions,
// project deletion) can never treat the key as collectable state.
func brunoLocalArtifactKeyPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return brunoLocalArtifactKeyFile
	}
	return filepath.Join(filepath.Dir(filepath.Clean(root)), brunoLocalArtifactKeyFile)
}

// brunoLocalArtifactKeyWrapper loads the controller's own envelope key,
// creating it on first use. Creation is O_EXCL so two controllers sharing a
// data directory converge on one key instead of overwriting each other's.
func brunoLocalArtifactKeyWrapper(root string) (keywrap.KeyWrapper, error) {
	path := brunoLocalArtifactKeyPath(root)
	key, err := readBrunoLocalArtifactKey(path)
	if err != nil {
		return nil, err
	}
	if key == nil {
		if key, err = createBrunoLocalArtifactKey(path); err != nil {
			return nil, err
		}
	}
	defer clear(key)
	return keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{
		Key:   key,
		KeyID: "local-file:" + brunoLocalArtifactKeyFile,
	})
}

func readBrunoLocalArtifactKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect local Bruno artifact key: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		slog.Warn("local Bruno artifact key is readable beyond its owner",
			"key_file", path, "mode", mode.String())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local Bruno artifact key: %w", err)
	}
	if len(key) < 32 {
		clear(key)
		return nil, errors.New("local Bruno artifact key is truncated; remove it to provision a new one, which makes previously imported Bruno sources unreadable")
	}
	return key, nil
}

func createBrunoLocalArtifactKey(path string) ([]byte, error) {
	if directory := filepath.Dir(path); directory != "" && directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create local Bruno artifact key directory: %w", err)
		}
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// Another controller (or an earlier racing request) won the create.
			clear(key)
			existing, readErr := readBrunoLocalArtifactKey(path)
			if readErr != nil {
				return nil, readErr
			}
			if existing == nil {
				return nil, errors.New("local Bruno artifact key disappeared while being provisioned")
			}
			return existing, nil
		}
		clear(key)
		return nil, fmt.Errorf("create local Bruno artifact key: %w", err)
	}
	writeErr := func() error {
		if _, err := file.Write(key); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		clear(key)
		return nil, fmt.Errorf("persist local Bruno artifact key: %w", err)
	}
	return key, nil
}

// writeBrunoSourceRetentionUnavailable answers with the operator action instead
// of writeError's generic 5xx mask. The body carries no internal error text, and
// a masked response here is what turned a fixable configuration gap into an
// import flow that offered every control and completed none of them.
func writeBrunoSourceRetentionUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": brunoSourceRetentionUnavailableMessage,
		"code":  brunoSourceRetentionUnavailableCode,
	})
}
