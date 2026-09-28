//go:build !linux

package javarunner

func RequirePID1Isolation() error { return ErrPID1Isolation }
func ReapPodOrphans(int) error    { return ErrPID1Isolation }

func RequireReadOnlyAsset(string) error { return ErrPID1Isolation }
