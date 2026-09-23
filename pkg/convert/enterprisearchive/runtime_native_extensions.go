package enterprisearchive

func nativeArchiveRuntimeDefinitions() []runtimeSettingSupportDefinition {
	rows := []struct {
		id, section, key, path, note string
		status                       RuntimeSettingStatus
	}{
		{"runtime_error_log_buffer", "Log", "AutoLogBufferSize", "config.log.error_buffer_bytes", "Bounded per-VUser error history; source KiB are converted to bytes. Process limits still apply.", RuntimeSettingStatusExecuted},
		{"http_receive_buffer", "WEB", "NetBufSize", "config.http.receive_buffer_bytes", "Native response-read buffer size; OS socket buffers and HTTP/2 flow control are independent, so identical LoadRunner transport behavior is not claimed.", RuntimeSettingStatusPartial},
		{"http_meta_refresh", "WEB", "MaxSelfMetaRefreshCount", "config.http.max_meta_refresh", "HTML meta-refresh visits per URL, bounded by a total of 50 navigations and the page deadline.", RuntimeSettingStatusExecuted},
		{"http_content_check_limit", "WEB", "MaxErrorMatchesAsERRORS", "config.http.content_check_error_limit", "Caps error-level failure matches for boundary-based content checks; subsequent matches are informational and the check still fails.", RuntimeSettingStatusExecuted},
	}
	out := make([]runtimeSettingSupportDefinition, 0, len(rows))
	for _, r := range rows {
		out = append(out, runtimeSettingSupportDefinition{entry: RuntimeSettingSupportEntry{ID: r.id, Area: "Runtime Settings", Concept: r.key, Fields: []string{r.path}, Status: r.status, Notes: r.note}, matchers: []runtimeSettingMatcher{matcher([]string{r.section}, []string{r.key})}})
	}
	return out
}
