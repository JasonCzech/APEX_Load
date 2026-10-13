// Package multiproject holds the black-box multi-project concurrency
// integration suite. It drives a deployed controller only through its REST
// API, so the same suite runs against the disposable Docker stack started by
// stack.py and against an existing AKS installation.
//
// The integration test is behind the multiproject_integration build tag and
// skips unless APEXLOAD_MP_BASE_URL is set. Its pure helpers are untagged, so
// their unit tests run with the ordinary go test ./... . See README.md in this
// directory.
package multiproject
