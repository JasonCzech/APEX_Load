package scripts_test

import (
	"bufio"
	"crypto/sha1" // #nosec G505 -- RFC 6455 mandates SHA-1 for Sec-WebSocket-Accept.
	"encoding/base64"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
)

func TestWebSocketSmokeValidatesRFC6455Handshake(t *testing.T) {
	tests := []struct {
		name        string
		response    func(string) string
		wantSuccess bool
	}{
		{
			name: "valid handshake",
			response: func(key string) string {
				accept := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) // #nosec G401 -- RFC 6455.
				return "HTTP/1.1 101 Switching Protocols\r\n" +
					"Upgrade: websocket\r\nConnection: keep-alive, Upgrade\r\n" +
					"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(accept[:]) + "\r\n\r\n"
			},
			wantSuccess: true,
		},
		{
			name: "bare 101 is rejected",
			response: func(string) string {
				return "HTTP/1.1 101 Switching Protocols\r\n\r\n"
			},
		},
		{
			name: "wrong accept is rejected",
			response: func(string) string {
				return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n" +
					"Connection: Upgrade\r\nSec-WebSocket-Accept: incorrect\r\n\r\n"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					done <- acceptErr
					return
				}
				defer connection.Close()
				reader := bufio.NewReader(connection)
				key := ""
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil {
						done <- readErr
						return
					}
					if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
						key = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					}
					if line == "\r\n" {
						break
					}
				}
				if key == "" {
					done <- fmt.Errorf("request did not contain Sec-WebSocket-Key")
					return
				}
				_, writeErr := connection.Write([]byte(test.response(key)))
				done <- writeErr
			}()

			command := exec.Command("python3", "deploy/websocket-smoke.py", "ws://"+listener.Addr().String()+"/api/v1/ws")
			output, commandErr := command.CombinedOutput()
			if serverErr := <-done; serverErr != nil {
				t.Fatalf("fake websocket server: %v", serverErr)
			}
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("valid handshake failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("invalid handshake passed:\n%s", output)
			}
		})
	}
}
