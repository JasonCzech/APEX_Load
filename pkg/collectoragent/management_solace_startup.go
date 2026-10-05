package collectoragent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

const solaceManagementQuarantineFile = "solace-sessions-v1.json"

// New calls this pure configuration gate before it advertises the selected
// observer. Loading remains under Run's process locks; preparation has no I/O.
func solaceManagementSpoolSelected(cfg Config) bool {
	for _, target := range cfg.AdapterTargets {
		if target.Pack == "solace-semp" && target.Management != nil && target.Management.Scope.Kind == analysis.ManagementKindSolace && hasConfiguredPack(cfg.Packs, "solace-semp") {
			return true
		}
	}
	return false
}
func validateSolaceManagementSpool(cfg Config) error {
	if solaceManagementSpoolSelected(cfg) && (cfg.SpoolDirectory == "" || !filepath.IsAbs(cfg.SpoolDirectory) || filepath.Clean(cfg.SpoolDirectory) != cfg.SpoolDirectory) {
		return &managementObservationError{reason: "permission_denied"}
	}
	return nil
}

// Agent.Run calls this after its existing spool lock, before starting any
// scheduler/heartbeat. One file and held lock per protected spool directory
// survive changes of controller URL, collector ID, source revision or pack.
// Previously unresolved records must load even when the Solace pack is off.
type solaceStartupHooks struct {
	openFile  func(string, int, os.FileMode) (*os.File, error)
	lockFile  func(context.Context, *os.File) (func() error, error)
	closeFile func(*os.File) error
}

func (a *Agent) lockSolaceManagementQuarantine(ctx context.Context) (func() error, error) {
	return a.lockSolaceManagementQuarantineWithHooks(ctx, solaceStartupHooks{openFile: os.OpenFile, lockFile: filelock.LockContextOwned, closeFile: func(file *os.File) error { return file.Close() }})
}
func (a *Agent) lockSolaceManagementQuarantineWithHooks(ctx context.Context, hooks solaceStartupHooks) (func() error, error) {
	if err := validateSolaceManagementSpool(a.cfg); err != nil {
		return nil, err
	}
	if a.cfg.SpoolDirectory == "" {
		return func() error { return nil }, nil
	}
	directory, err := filepath.Abs(a.cfg.SpoolDirectory)
	if err != nil || hooks.openFile == nil || hooks.lockFile == nil || hooks.closeFile == nil || ctx == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	if err := managementContextError(ctx); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(directory, solaceManagementQuarantineFile+".lock")
	owner, err := reserveSolaceStartupOwner(lockPath, hooks.closeFile)
	if err != nil {
		return nil, err
	}
	return runSolaceStartupOwned(ctx, owner, func(ctx context.Context, owner *solaceStartupOwner) error {
		if !solaceManagementSpoolSelected(a.cfg) {
			_, statErr := os.Lstat(filepath.Join(directory, solaceManagementQuarantineFile))
			if os.IsNotExist(statErr) {
				return nil
			}
			if statErr != nil {
				return &managementObservationError{reason: "invalid"}
			}
		}
		if err := managementContextError(ctx); err != nil {
			return err
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return &managementObservationError{reason: "invalid"}
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return &managementObservationError{reason: "permission_denied"}
		}
		if err := managementContextError(ctx); err != nil {
			return err
		}
		file, err := hooks.openFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
		if file != nil {
			owner.ownFile(file)
		}
		if err != nil || file == nil {
			return &managementObservationError{reason: "invalid"}
		}
		opened, statErr := file.Stat()
		linked, linkErr := os.Lstat(lockPath)
		if statErr != nil || linkErr != nil || !linked.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(opened, linked) || opened.Mode().Perm()&0077 != 0 {
			return &managementObservationError{reason: "permission_denied"}
		}
		if err := managementContextError(ctx); err != nil {
			return err
		}
		release, err := hooks.lockFile(ctx, file)
		owner.ownRelease(release) // Retain acquired authority even on late cancellation.
		if err != nil {
			return err
		}
		if release == nil {
			return errSolaceStartupOwnerUnproven
		}
		if err := managementContextError(ctx); err != nil {
			return err
		}
		q, err := openSolaceManagementQuarantine(filepath.Join(directory, solaceManagementQuarantineFile), a.spoolBudget)
		if err != nil {
			return err
		}
		if a.management != nil {
			if err = a.management.origins.bindSolaceQuarantine(q); err != nil {
				return err
			}
		}
		return nil
	})
}
