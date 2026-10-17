package recordbridge

import "net/http"

func (s *Bridge) routes() {
	bridge := s.pairing
	if bridge == nil {
		return
	}
	cors := bridge.corsMiddleware
	log := bridgeFailureLoggingMiddleware
	auth := bridge.authMiddleware

	s.mux.Handle("GET "+bridgeSystemHealthPath, cors(log(http.HandlerFunc(s.health))))
	s.mux.Handle("POST "+bridgePairPath, cors(log(http.HandlerFunc(bridge.handlePair))))
	s.mux.Handle("GET "+bridgeStatusPath, cors(log(auth(http.HandlerFunc(bridge.handleStatus)))))
	s.mux.Handle("GET "+bridgeWebSocketPath, log(auth(http.HandlerFunc(s.hub.ServeHTTP))))
	s.mux.Handle("GET /api/v1/browser-replay/sessions/{session_id}", cors(log(auth(http.HandlerFunc(s.service.HandleGetBrowserReplaySession)))))

	s.mux.Handle("GET /api/v1/browser-recorder/sessions", cors(log(auth(http.HandlerFunc(s.service.HandleListBrowserRecordings)))))
	s.mux.Handle("GET /api/v1/browser-recorder/storage-states", cors(log(auth(http.HandlerFunc(s.service.HandleListBrowserRecordingStorageStates)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions", cors(log(auth(http.HandlerFunc(s.service.HandleStartBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}", cors(log(auth(http.HandlerFunc(s.service.HandleGetBrowserRecording)))))
	s.mux.Handle("DELETE /api/v1/browser-recorder/sessions/{id}", cors(log(auth(http.HandlerFunc(s.service.HandleDeleteBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/stop", cors(log(auth(http.HandlerFunc(s.service.HandleStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/force-stop", cors(log(auth(http.HandlerFunc(s.service.HandleForceStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pause", cors(log(auth(http.HandlerFunc(s.service.HandlePauseBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/resume", cors(log(auth(http.HandlerFunc(s.service.HandleResumeBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/navigate", cors(log(auth(http.HandlerFunc(s.service.HandleNavigateBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/focus", cors(log(auth(http.HandlerFunc(s.service.HandleFocusBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/control-panel", cors(log(auth(http.HandlerFunc(s.service.HandleOpenBrowserRecordingControlPanel)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/start", cors(log(auth(http.HandlerFunc(s.service.HandleStartBrowserRecordingTransaction)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/end", cors(log(auth(http.HandlerFunc(s.service.HandleEndBrowserRecordingTransaction)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/transactions/suggestions", cors(log(auth(http.HandlerFunc(s.service.HandleSuggestBrowserRecordingTransactions)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/notes", cors(log(auth(http.HandlerFunc(s.service.HandleAddBrowserRecordingNote)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/section", cors(log(auth(http.HandlerFunc(s.service.HandleSetBrowserRecordingSection)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pick-mode", cors(log(auth(http.HandlerFunc(s.service.HandleSetBrowserRecordingPickMode)))))
	s.mux.Handle("DELETE /api/v1/browser-recorder/sessions/{id}/steps/last", cors(log(auth(http.HandlerFunc(s.service.HandleDeleteLastBrowserRecordingStep)))))
	s.mux.Handle("PATCH /api/v1/browser-recorder/sessions/{id}/steps/{seq}", cors(log(auth(http.HandlerFunc(s.service.HandleUpdateBrowserRecordingStep)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/verify", cors(log(auth(http.HandlerFunc(s.service.HandleVerifyBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert/preview", cors(log(auth(http.HandlerFunc(s.service.HandlePreviewBrowserRecordingConversion)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert", cors(log(auth(http.HandlerFunc(s.service.HandleConvertBrowserRecording)))))
	// The agent mints the conversion preview's import draft, so the review
	// steps that change that draft (credential bindings, parameterization) run
	// here too; the controller cannot vouch for a draft it did not create.
	s.mux.Handle("POST /api/v1/scripts/secret-bindings/apply", cors(log(auth(http.HandlerFunc(s.service.HandleApplyScriptSecretBindings)))))
	s.mux.Handle("POST /api/v1/scripts/import/parameterize/apply", cors(log(auth(http.HandlerFunc(s.service.HandleApplyImportParameterization)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/export", cors(log(auth(http.HandlerFunc(s.service.HandleExportBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/storage-state", cors(log(auth(http.HandlerFunc(s.service.HandleExportBrowserRecordingStorageState)))))
	s.mux.Handle("GET /api/v1/browser-recorder/devices", cors(log(auth(http.HandlerFunc(s.service.HandleListBrowserRecorderDevices)))))
	s.mux.Handle("POST /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.service.HandleBrowserRecorderInstall)))))
	s.mux.Handle("GET /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.service.HandleGetBrowserRecorderInstall)))))
	// Device capture: the agent's proxy records a phone or tablet, and the
	// proxy recording routes the controller serves run here for it.
	s.mux.Handle("GET /api/v1/device-capture", cors(log(auth(http.HandlerFunc(s.service.HandleGetDeviceCapture)))))
	s.mux.Handle("POST /api/v1/device-capture", cors(log(auth(http.HandlerFunc(s.service.HandleStartDeviceCapture)))))
	s.mux.Handle("POST /api/v1/device-capture/stop", cors(log(auth(http.HandlerFunc(s.service.HandleStopDeviceCapture)))))
	s.mux.Handle("GET /api/v1/device-capture/qr.svg", cors(log(auth(http.HandlerFunc(s.service.HandleDeviceCaptureQRCode)))))
	s.mux.Handle("GET /api/v1/recorder/{id}", cors(log(auth(http.HandlerFunc(s.service.HandleGetRecording)))))
	s.mux.Handle("PATCH /api/v1/recorder/{id}/requests/{seq}", cors(log(auth(http.HandlerFunc(s.service.HandleUpdateRecordedRequest)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/transactions/start", cors(log(auth(http.HandlerFunc(s.service.HandleStartRecordingTransaction)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/transactions/end", cors(log(auth(http.HandlerFunc(s.service.HandleEndRecordingTransaction)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/notes", cors(log(auth(http.HandlerFunc(s.service.HandleAddRecordingNote)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert/preview", cors(log(auth(http.HandlerFunc(s.service.HandlePreviewRecordingConversion)))))
	s.mux.Handle("GET /api/v1/recorder/{id}/export", cors(log(auth(http.HandlerFunc(s.service.HandleExportRecording)))))
	s.mux.Handle("OPTIONS /api/", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
}
