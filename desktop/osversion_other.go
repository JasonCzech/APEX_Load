//go:build !darwin

package main

func operatingSystemVersion() string {
	return ""
}
