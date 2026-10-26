//go:build !darwin && !linux && !windows

package procidentity

func readHost() (Host, error) { return Host{}, ErrUnavailable }

func processStart(int) (string, bool, error) { return "", false, ErrUnavailable }
