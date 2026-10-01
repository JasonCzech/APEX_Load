package collectoragent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"golang.org/x/crypto/ssh"
)

// The operator cannot substitute shell text; every SSH job uses this fixed pack.
const linuxCommandPack = "LC_ALL=C; cat /proc/loadavg; cat /proc/meminfo; cat /proc/uptime"

type limitedOutput struct {
	bytes.Buffer
	maximum int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > b.maximum-b.Len() {
		return 0, errors.New("monitor response exceeds limit")
	}
	return b.Buffer.Write(p)
}

func collectSSH(ctx context.Context, target AdapterTarget, credentials Credentials) ([]analysis.MetricPoint, error) {
	endpoint, err := url.Parse(target.Destination)
	if err != nil || endpoint.Scheme != "ssh" || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" {
		return nil, errors.New("invalid approved SSH endpoint")
	}
	if credentials.Username == "" || len(credentials.Username) > 128 {
		return nil, errors.New("SSH username required")
	}
	pinned, _, _, _, err := ssh.ParseAuthorizedKey([]byte(target.HostKey))
	if err != nil {
		return nil, errors.New("pinned SSH host key required")
	}
	var auth ssh.AuthMethod
	if credentials.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(credentials.PrivateKey))
		if err != nil {
			return nil, errors.New("invalid SSH key")
		}
		auth = ssh.PublicKeys(signer)
	} else {
		if credentials.Password == "" {
			return nil, errors.New("SSH credentials required")
		}
		auth = ssh.Password(credentials.Password)
	}
	address := endpoint.Host
	if endpoint.Port() == "" {
		address = net.JoinHostPort(endpoint.Hostname(), "22")
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, errors.New("SSH connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	sshConn, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: credentials.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: ssh.FixedHostKey(pinned)})
	if err != nil {
		return nil, errors.New("SSH identity or authentication failed")
	}
	client := ssh.NewClient(sshConn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, errors.New("SSH session failed")
	}
	defer session.Close()
	output := &limitedOutput{maximum: 64 << 10}
	session.Stdout = output
	session.Stderr = &limitedOutput{maximum: 4096}
	if err = session.Run(linuxCommandPack); err != nil {
		return nil, errors.New("fixed Linux command pack failed")
	}
	return parseLinuxCommandPack(output.String(), time.Now().UTC())
}
func parseLinuxCommandPack(body string, at time.Time) ([]analysis.MetricPoint, error) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 3 {
		return nil, errors.New("incomplete Linux command response")
	}
	var points []analysis.MetricPoint
	add := func(name, unit, text string, scale float64) error {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("invalid Linux numeric response")
		}
		v *= scale
		points = append(points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, Source: "ssh", TimestampUTC: at, MetricName: name, Unit: unit, Kind: analysis.KindGauge, Value: &v, Quality: analysis.QualityValid})
		return nil
	}
	load := strings.Fields(lines[0])
	if len(load) < 3 {
		return nil, errors.New("incomplete load response")
	}
	for i := 0; i < 3; i++ {
		if err := add(fmt.Sprintf("system.load.%dm", []int{1, 5, 15}[i]), "1", load[i], 1); err != nil {
			return nil, err
		}
	}
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Fields(line)
		if len(fields) == 3 && (fields[0] == "MemTotal:" || fields[0] == "MemAvailable:") && fields[2] == "kB" {
			name := "system.memory.total"
			if fields[0] == "MemAvailable:" {
				name = "system.memory.available"
			}
			if err := add(name, "By", fields[1], 1024); err != nil {
				return nil, err
			}
		}
	}
	uptime := strings.Fields(lines[len(lines)-1])
	if len(uptime) != 2 {
		return nil, errors.New("incomplete uptime response")
	}
	if err := add("system.uptime", "s", uptime[0], 1); err != nil {
		return nil, err
	}
	if len(points) != 6 {
		return nil, errors.New("required Linux metrics missing")
	}
	return points, nil
}
