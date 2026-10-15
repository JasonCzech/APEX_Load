package api

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
type deviceCapture struct {
	mu        sync.Mutex
	sessionID string
	name      string
	host      string
	port      int
	password  string
}

// deviceCaptureInterface is a workstation network address a device on the
// same network can reach.
type deviceCaptureInterface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type deviceCaptureView struct {
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
var deviceCaptureInterfaces = func() []deviceCaptureInterface {
	out := []deviceCaptureInterface{}
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
			out = append(out, deviceCaptureInterface{Name: iface.Name, Address: network.IP.String()})
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
func (d *deviceCapture) view(rec *recorder.Recorder) *deviceCaptureView {
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
	view := &deviceCaptureView{
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
func (s *Server) handleGetDeviceCapture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces": deviceCaptureInterfaces(),
		"capture":    s.recordingBridge.device.view(s.recorder),
	})
}

// handleStartDeviceCapture handles POST /api/v1/device-capture: it binds the
// proxy to the chosen address with a fresh password and starts recording.
func (s *Server) handleStartDeviceCapture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string   `json:"name"`
		Address      string   `json:"address"`
		Port         int      `json:"port,omitempty"`
		IncludeHosts []string `json:"include_hosts,omitempty"`
		ExcludeHosts []string `json:"exclude_hosts,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
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
		writeError(w, http.StatusBadRequest, "port must be between 1024 and 65535")
		return
	}
	address := strings.TrimSpace(req.Address)
	known := false
	for _, iface := range deviceCaptureInterfaces() {
		known = known || iface.Address == address
	}
	if !known {
		writeError(w, http.StatusBadRequest, "address must be one of this workstation's network addresses")
		return
	}

	device := s.recordingBridge.device
	device.mu.Lock()
	if device.sessionID != "" {
		if session, ok := s.recorder.GetSessionWindow(device.sessionID, false, 0, 0); ok && session.Status != "stopped" {
			device.mu.Unlock()
			writeError(w, http.StatusConflict, "a device capture is already running; stop it first")
			return
		}
	}
	password, err := newDeviceCapturePassword()
	if err == nil {
		err = s.recorder.Listen(net.JoinHostPort(address, strconv.Itoa(req.Port)), password)
		if err != nil && !explicitPort {
			// The default port is taken: any free port does, and the
			// device is told which.
			err = s.recorder.Listen(net.JoinHostPort(address, "0"), password)
		}
		if err != nil {
			err = fmt.Errorf("the recording proxy cannot listen on %s: %w", address, err)
		}
	}
	port := req.Port
	if _, bound, splitErr := net.SplitHostPort(s.recorder.ProxyAddr()); err == nil && splitErr == nil {
		port, _ = strconv.Atoi(bound)
	}
	id := uuid.New().String()
	if err == nil {
		if _, err = s.recorder.StartSession(id, req.Name, recorder.SessionFilters{
			IncludeHosts:     req.IncludeHosts,
			ExcludeHosts:     req.ExcludeHosts,
			ExcludeStatic:    true,
			ExcludeStaticSet: true,
		}, nil); err != nil {
			_ = s.recorder.CloseListener()
		}
	}
	if err != nil {
		device.mu.Unlock()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	device.sessionID, device.name, device.host, device.port, device.password = id, req.Name, address, port, password
	device.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"capture": device.view(s.recorder)})
}

// handleStopDeviceCapture handles POST /api/v1/device-capture/stop: it ends
// the recording and stops the proxy listening.
func (s *Server) handleStopDeviceCapture(w http.ResponseWriter, r *http.Request) {
	device := s.recordingBridge.device
	device.mu.Lock()
	sessionID := device.sessionID
	device.mu.Unlock()
	if sessionID == "" {
		writeError(w, http.StatusNotFound, "no device capture is running")
		return
	}
	if _, err := s.recorder.StopSession(sessionID); err != nil {
		// Stopping a stopped capture again is no error.
		if session, ok := s.recorder.GetSessionWindow(sessionID, false, 0, 0); !ok || session.Status != "stopped" {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	_ = s.recorder.CloseListener()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"capture": s.recordingBridge.device.view(s.recorder)})
}

// handleDeviceCaptureQRCode handles GET /api/v1/device-capture/qr.svg: the
// setup page's address as a QR code the device's camera opens.
func (s *Server) handleDeviceCaptureQRCode(w http.ResponseWriter, r *http.Request) {
	view := s.recordingBridge.device.view(s.recorder)
	if view == nil || view.Status == "stopped" {
		writeError(w, http.StatusNotFound, "no device capture is running")
		return
	}
	code, err := qrcode.Encode(view.SetupURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(code.SVG(8)))
}
