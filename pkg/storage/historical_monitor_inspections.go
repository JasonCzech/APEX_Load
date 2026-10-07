package storage

type AppDynamicsInspection = MonitorInspection

func (s *SQLiteStore) GetAppDynamicsInspection(id string) (*AppDynamicsInspection, error) {
	return scanMonitorInspection(s.db.QueryRow(`SELECT `+monitorInspectionColumns+` FROM appdynamics_inspections WHERE id=? AND provider='appdynamics'`, id))
}
