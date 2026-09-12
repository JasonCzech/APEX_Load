//go:build jms_solace_real

package jms

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Observe the actual SASL wire identity, without a live broker. Solace VPN
// routing is determined by the endpoint; a configured VPN must not mutate the
// supplied client username, including usernames that already contain '@'.
func TestSolaceAMQPVPNPreservesSASLIdentity(t *testing.T) {
	for _, username := range []string{"fixture-user", "fixture-user@tenant", " fixture-user "} {
		t.Run(username, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			type observed struct {
				payload []byte
				err     error
			}
			result := make(chan observed, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					result <- observed{err: err}
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				header := make([]byte, 8)
				if _, err = io.ReadFull(conn, header); err != nil {
					result <- observed{err: err}
					return
				}
				if !bytes.Equal(header, []byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0}) {
					result <- observed{err: fmt.Errorf("expected SASL protocol header")}
					return
				}
				if _, err = conn.Write(header); err != nil {
					result <- observed{err: err}
					return
				}
				// AMQP 1.0 SASL-MECHANISMS: descriptor 0x40, one symbol array
				// containing PLAIN. This peer intentionally ends after SASL-INIT.
				frame := []byte{0, 0, 0, 24, 2, 1, 0, 0, 0, 0x53, 0x40, 0xc0, 11, 1, 0xe0, 8, 1, 0xa3, 5, 'P', 'L', 'A', 'I', 'N'}
				if _, err = conn.Write(frame); err != nil {
					result <- observed{err: err}
					return
				}
				if _, err = io.ReadFull(conn, header); err != nil {
					result <- observed{err: err}
					return
				}
				size := binary.BigEndian.Uint32(header[:4])
				if size < 8 || size > 4096 {
					result <- observed{err: fmt.Errorf("invalid SASL frame size %d", size)}
					return
				}
				payload := make([]byte, int(size)-8)
				_, err = io.ReadFull(conn, payload)
				result <- observed{payload: payload, err: err}
			}()
			adapter := newSolaceAdapter(config{BrokerURL: "amqp://" + listener.Addr().String(), Username: username, Password: "fixture-password", MessageVPN: "performance"})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			// The peer closes before SASL-OUTCOME, so a connection error is expected.
			if err := adapter.HealthCheck(ctx); err == nil {
				t.Fatal("incomplete handshake unexpectedly succeeded")
			}
			select {
			case got := <-result:
				if got.err != nil {
					t.Fatal(got.err)
				}
				want := []byte("\x00" + strings.TrimSpace(username) + "\x00fixture-password")
				if !bytes.Contains(got.payload, want) {
					t.Fatal("SASL username was rewritten by the VPN setting")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("SASL peer did not complete")
			}
		})
	}
}
