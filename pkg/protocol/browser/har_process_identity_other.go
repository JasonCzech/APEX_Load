//go:build !darwin && !linux

package browser

func readHARHost() (harHost, error) { return harHost{}, errHARProcessUnavailable }

func harProcessStart(int) (string, bool, error) { return "", false, errHARProcessUnavailable }
