package recordbridge

import (
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"github.com/jasonczech/apexload/pkg/scrub"
	"net/http"
	"time"
)

// Small exported projections let the controller retain its established helper
// names while both entry points use one implementation.
func BrowserStorageStateProjectDir(projectID string) string {
	return browserStorageStateProjectDir(projectID)
}
func ConfineStorageStatePath(raw, projectID string) (string, error) {
	return confineStorageStatePath(raw, projectID)
}
func FilepathSplitForward(path string) (string, string) { return filepathSplitForward(path) }
func BrowserStorageStateExportLocation(info *BrowserRecordingSession) (string, string) {
	return browserStorageStateExportLocation(info)
}
func PruneBrowserStorageStateExports(now time.Time, retention time.Duration) {
	pruneBrowserStorageStateExports(now, retention)
}
func ScrubBrowserStorageState(data []byte) ([]byte, error) { return scrubBrowserStorageState(data) }
func ScrubBrowserStorageStateNode(node any, parentKey string) {
	scrubBrowserStorageStateNode(node, parentKey)
}
func BrowserStorageStateSensitiveName(name string) bool {
	return browserStorageStateSensitiveName(name)
}
func ValidateBrowserRecordingStartURL(raw string) error { return validateBrowserRecordingStartURL(raw) }
func (seed *BrowserRecordingBridgeConnectionSeed) LocalStorageEntry(panelURL string) (string, string, error) {
	return seed.localStorageEntry(panelURL)
}
func AbsoluteHTTPOrigin(raw string) (string, error) { return absoluteHTTPOrigin(raw) }
func BrowserRecordingNavigationFailure(err error, startURL string) (string, bool) {
	return browserRecordingNavigationFailure(err, startURL)
}
func BrowserRecordingAvailabilityFromStartError(err error) (BrowserRecordingAvailability, bool) {
	return browserRecordingAvailabilityFromStartError(err)
}
func SanitizeRecorderSessionForResponse(session *recorder.Session) *recorder.Session {
	return sanitizeRecorderSessionForResponse(session)
}
func SanitizeRecorderWarningsForResponse(warnings []string) []string {
	return sanitizeRecorderWarningsForResponse(warnings)
}
func SanitizeRecordedRequestForResponse(request recorder.RecordedRequest) recorder.RecordedRequest {
	return sanitizeRecordedRequestForResponse(request)
}
func SanitizeRecordedHeaderValues(headers map[string][]string, policy scrub.Policy) map[string][]string {
	return sanitizeRecordedHeaderValues(headers, policy)
}
func RawRecordingExportRequested(r *http.Request) bool { return rawRecordingExportRequested(r) }
func SanitizeDownloadName(name string) string          { return sanitizeDownloadName(name) }
func MergeRecorderHostFilters(profileHosts, requestHosts []string) []string {
	return mergeRecorderHostFilters(profileHosts, requestHosts)
}
func RecorderCaptureCookies(requested *bool, profile *recordingprofile.Profile) bool {
	return recorderCaptureCookies(requested, profile)
}
