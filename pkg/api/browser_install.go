package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

type browserInstallSnapshot = recordbridge.InstallSnapshot
type browserInstallState struct{ *recordbridge.InstallState }

func newBrowserInstallState() *browserInstallState {
	return &browserInstallState{recordbridge.NewInstallState()}
}
func (b *browserInstallState) start(install func() error) browserInstallSnapshot {
	return b.Start(install)
}
func (b *browserInstallState) snapshot() browserInstallSnapshot { return b.Snapshot() }
