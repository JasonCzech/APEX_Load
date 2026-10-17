package recordbridge

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jasonczech/apexload/pkg/qrcode"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

// A device capture records a phone or tablet through the Recording Agent: its
// recording proxy listens on the workstation's network address with a fresh
// password, serves the device its setup page and certificate, and records
// what the device's apps send. Production controllers keep their own proxy
// off; the agent runs on the tester's workstation (decision D9).

const (
	// deviceCaptureOriginKey marks, in a script's preserved import metadata,
	// a script the Recording Agent converted from a device capture.
	deviceCaptureOriginKey = "recording_origin"
	deviceCaptureOrigin    = "device_capture"
	deviceCaptureUsername  = "apexload"
	// deviceCapturePasswordAlphabet leaves out look-alike characters, since the
	// password is typed on the device.
	deviceCapturePasswordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	deviceCapturePasswordGroups   = 4
	deviceCapturePasswordGroupLen = 4
)

// defaultDeviceCapturePort is where a capture listens unless asked
// otherwise; when it is taken, any free port does.
var defaultDeviceCapturePort = 8888

// deviceCapture is the agent's current device capture.
type DeviceCapture struct {
	mu        sync.Mutex
	sessionID string
	name      string
	host      string
	port      int
	password  string
	// profile is the project recording profile the capture records and
	// converts with: the agent has no project store, so the dashboard hands
	// over the one selected in Recording Studio.
	profile *recordingprofile.Profile
}

// recordingProfile is the profile of the capture with this session, or nil.
func (d *DeviceCapture) recordingProfile(sessionID string) *recordingprofile.Profile {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.profile == nil || d.sessionID == "" || d.sessionID != sessionID {
		return nil
	}
	profile := *d.profile
	return &profile
}

// DeviceCaptureInterface is a workstation network address a device on the
// same network can reach.
type DeviceCaptureInterface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type DeviceCaptureView struct {
	SessionID        string     `json:"session_id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	Username         string     `json:"username"`
	Password         string     `json:"password"`
	SetupURL         string     `json:"setup_url"`
	CAURL            string     `json:"ca_url"`
	StartedAt        time.Time  `json:"started_at"`
	StoppedAt        *time.Time `json:"stopped_at,omitempty"`
	RequestCount     int        `json:"request_count"`
	RejectedRequests int64      `json:"rejected_requests,omitempty"`
	LastRejectedHost string     `json:"last_rejected_host,omitempty"`
	FilteredRequests int64      `json:"filtered_requests,omitempty"`
	LastFilteredHost string     `json:"last_filtered_host,omitempty"`
	LastHost         string     `json:"last_host,omitempty"`
	OpenTransactions []string   `json:"open_transactions"`
	// Warnings are the session's, such as a transaction the stop closed.
	Warnings []string `json:"warnings,omitempty"`
}

// deviceCaptureInterfaces lists the workstation's IPv4 addresses on up,
// non-loopback interfaces.
var deviceCaptureInterfaces = func() []DeviceCaptureInterface {
	out := []DeviceCaptureInterface{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			network, ok := addr.(*net.IPNet)
			if !ok || network.IP.To4() == nil || !network.IP.IsGlobalUnicast() {
				continue
			}
			out = append(out, DeviceCaptureInterface{Name: iface.Name, Address: network.IP.String()})
		}
	}
	return out
}

func newDeviceCapturePassword() (string, error) {
	var groups []string
	for range deviceCapturePasswordGroups {
		group := make([]byte, deviceCapturePasswordGroupLen)
		for i := range group {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(deviceCapturePasswordAlphabet))))
			if err != nil {
				return "", err
			}
			group[i] = deviceCapturePasswordAlphabet[n.Int64()]
		}
		groups = append(groups, string(group))
	}
	return strings.Join(groups, "-"), nil
}

// view describes the capture with its session's live counts; nil when there
// is none.
func (d *DeviceCapture) view(rec *recorder.Recorder) *DeviceCaptureView {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sessionID == "" {
		return nil
	}
	session, ok := rec.GetSessionWindow(d.sessionID, true, 0, 1)
	if !ok {
		return nil
	}
	hostPort := net.JoinHostPort(d.host, strconv.Itoa(d.port))
	view := &DeviceCaptureView{
		SessionID:        d.sessionID,
		Name:             d.name,
		Status:           session.Status,
		Host:             d.host,
		Port:             d.port,
		Username:         deviceCaptureUsername,
		Password:         d.password,
		SetupURL:         "http://" + hostPort + "/",
		CAURL:            "http://" + hostPort + recorder.DeviceSetupCertificatePath,
		StartedAt:        session.StartedAt,
		StoppedAt:        session.StoppedAt,
		RequestCount:     session.RequestCount,
		RejectedRequests: session.RejectedRequests,
		LastRejectedHost: session.LastRejectedHost,
		FilteredRequests: session.FilteredRequests,
		LastFilteredHost: session.LastFilteredHost,
		OpenTransactions: append([]string{}, session.OpenTransactions...),
		Warnings:         sanitizeRecorderWarningsForResponse(session.Warnings),
	}
	if len(session.Requests) > 0 {
		view.LastHost = session.Requests[len(session.Requests)-1].Host
	}
	if session.Status == "stopped" {
		// A stopped capture's proxy no longer listens; its password is spent.
		view.Password = ""
	}
	return view
}

// handleGetDeviceCapture handles GET /api/v1/device-capture: the
// workstation's addresses and the current capture.
func (s *Service) HandleGetDeviceCapture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces": deviceCaptureInterfaces(),
		"capture":    s.Device.view(s.Recorder),
	})
}

// handleStartDeviceCapture handles POST /api/v1/device-capture: it binds the
// proxy to the chosen address with a fresh password and starts recording.
func (s *Service) HandleStartDeviceCapture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string   `json:"name"`
		Address      string   `json:"address"`
		Port         int      `json:"port,omitempty"`
		IncludeHosts []string `json:"include_hosts,omitempty"`
		ExcludeHosts []string `json:"exclude_hosts,omitempty"`
		// RecordingProfile is the project recording profile selected in
		// Recording Studio: its host filters, capture limit and cookie
		// capture apply to the recording, and its code generation settings
		// to the conversion.
		RecordingProfile *recordingprofile.Profile `json:"recording_profile,omitempty"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	profile := req.RecordingProfile
	if profile != nil {
		if err := recordingprofile.Validate([]recordingprofile.Profile{*profile}); err != nil {
			s.writeError(w, http.StatusBadRequest, strings.Replace(err.Error(), "recording_profiles[0]", "recording_profile", 1))
			return
		}
	}
	filters := recorder.SessionFilters{
		IncludeHosts:     req.IncludeHosts,
		ExcludeHosts:     req.ExcludeHosts,
		ExcludeStatic:    true,
		ExcludeStaticSet: true,
	}
	if profile != nil {
		// As a proxy recording on the controller applies its profile.
		filters.IncludeHosts = mergeRecorderHostFilters(profile.IncludeHosts, req.IncludeHosts)
		filters.ExcludeHosts = mergeRecorderHostFilters(profile.ExcludeHosts, req.ExcludeHosts)
		filters.BodyCaptureLimitBytes = profile.BodyCaptureLimitBytes
		filters.CaptureCookies = recorderCaptureCookies(nil, profile)
		filters.RecordingProfileID = profile.ID
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Device capture"
	}
	explicitPort := req.Port != 0
	if !explicitPort {
		req.Port = defaultDeviceCapturePort
	}
	if req.Port < 1024 || req.Port > 65535 {
		s.writeError(w, http.StatusBadRequest, "port must be between 1024 and 65535")
		return
	}
	address := strings.TrimSpace(req.Address)
	known := false
	for _, iface := range deviceCaptureInterfaces() {
		known = known || iface.Address == address
	}
	if !known {
		s.writeError(w, http.StatusBadRequest, "address must be one of this workstation's network addresses")
		return
	}

	device := s.Device
	device.mu.Lock()
	if device.sessionID != "" {
		if session, ok := s.Recorder.GetSessionWindow(device.sessionID, false, 0, 0); ok && session.Status != "stopped" {
			device.mu.Unlock()
			s.writeError(w, http.StatusConflict, "a device capture is already running; stop it first")
			return
		}
	}
	password, err := newDeviceCapturePassword()
	if err == nil {
		err = s.Recorder.Listen(net.JoinHostPort(address, strconv.Itoa(req.Port)), password)
		if err != nil && !explicitPort {
			// The default port is taken: any free port does, and the
			// device is told which.
			err = s.Recorder.Listen(net.JoinHostPort(address, "0"), password)
		}
		if err != nil {
			err = fmt.Errorf("the recording proxy cannot listen on %s: %w", address, err)
		}
	}
	port := req.Port
	if _, bound, splitErr := net.SplitHostPort(s.Recorder.ProxyAddr()); err == nil && splitErr == nil {
		port, _ = strconv.Atoi(bound)
	}
	id := uuid.New().String()
	if err == nil {
		if _, err = s.Recorder.StartSession(id, req.Name, filters, nil); err != nil {
			_ = s.Recorder.CloseListener()
		}
	}
	if err != nil {
		device.mu.Unlock()
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	device.sessionID, device.name, device.host, device.port, device.password = id, req.Name, address, port, password
	device.profile = profile
	device.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"capture": device.view(s.Recorder)})
}

// handleStopDeviceCapture handles POST /api/v1/device-capture/stop: it ends
// the recording and stops the proxy listening.
func (s *Service) HandleStopDeviceCapture(w http.ResponseWriter, r *http.Request) {
	device := s.Device
	device.mu.Lock()
	sessionID := device.sessionID
	device.mu.Unlock()
	if sessionID == "" {
		s.writeError(w, http.StatusNotFound, "no device capture is running")
		return
	}
	if _, err := s.Recorder.StopSession(sessionID); err != nil {
		// Stopping a stopped capture again is no error.
		if session, ok := s.Recorder.GetSessionWindow(sessionID, false, 0, 0); !ok || session.Status != "stopped" {
			s.writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	_ = s.Recorder.CloseListener()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"capture": s.Device.view(s.Recorder)})
}

// handleDeviceCaptureQRCode handles GET /api/v1/device-capture/qr.svg: the
// setup page's address as a QR code the device's camera opens.
func (s *Service) HandleDeviceCaptureQRCode(w http.ResponseWriter, r *http.Request) {
	view := s.Device.view(s.Recorder)
	if view == nil || view.Status == "stopped" {
		s.writeError(w, http.StatusNotFound, "no device capture is running")
		return
	}
	code, err := qrcode.Encode(view.SetupURL)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(code.SVG(8)))
}
