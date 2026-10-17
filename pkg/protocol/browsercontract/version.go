package browsercontract

import (
	"regexp"
	"strconv"
	"strings"
)

var browserVersionPattern = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,6}){0,3}$`)

func ValidBrowserVersion(version string) bool {
	return browserVersionPattern.MatchString(version)
}
func BrowserMajorVersion(version string) (int, bool) {
	if !ValidBrowserVersion(version) {
		return 0, false
	}
	major, _, _ := strings.Cut(version, ".")
	value, err := strconv.Atoi(major)
	return value, err == nil
}
