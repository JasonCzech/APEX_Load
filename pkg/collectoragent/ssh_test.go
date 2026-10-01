package collectoragent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestFixedLinuxCommandPackParsing(t *testing.T) {
	fixture := "0.10 0.20 0.30 1/20 99\nMemTotal: 100 kB\nMemFree: 1 kB\nMemAvailable: 70 kB\n10.0 20.0\n"
	points, err := parseLinuxCommandPack(fixture, time.Now().UTC())
	if err != nil || len(points) != 6 {
		t.Fatalf("parse: %v %+v", err, points)
	}
	if *points[3].Value != 102400 || *points[4].Value != 71680 {
		t.Fatal("memory scale incorrect")
	}
	for _, bad := range []string{"", "1 2 3\nMemTotal: 100 kB\n1 2", "NaN 2 3\nMemTotal: 100 kB\nMemAvailable: 70 kB\n1 2"} {
		_, e := parseLinuxCommandPack(bad, time.Now().UTC())
		if e == nil {
			t.Fatal("malformed output accepted")
		}
	}
}

// The fixture is a real SSH transport. It verifies the fixed command and that a
// wrong host key is rejected before password authentication or command dispatch.
func TestSSHCommandPackPinnedIdentity(t *testing.T) {
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(hostKey)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var commands atomic.Int32
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "monitor" && string(p) == "secret" {
			return nil, nil
		}
		return nil, fmt.Errorf("denied")
	}}
	config.AddHostKey(signer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				server, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for request := range channels {
					channel, reqs, e := request.Accept()
					if e != nil {
						return
					}
					for r := range reqs {
						if r.Type != "exec" {
							r.Reply(false, nil)
							continue
						}
						var command struct{ Value string }
						if ssh.Unmarshal(r.Payload, &command) != nil || command.Value != linuxCommandPack {
							r.Reply(false, nil)
							channel.Close()
							continue
						}
						commands.Add(1)
						r.Reply(true, nil)
						channel.Write([]byte("0.1 0.2 0.3 1/20 99\nMemTotal: 100 kB\nMemAvailable: 70 kB\n10 20\n"))
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						channel.Close()
					}
				}
			}()
		}
	}()
	target := AdapterTarget{Pack: "ssh-linux", Destination: "ssh://" + listener.Addr().String(), HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
	points, err := collectSSH(context.Background(), target, Credentials{Username: "monitor", Password: "secret"})
	if err != nil || len(points) != 6 {
		t.Fatalf("SSH: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	wrong, _ := ssh.NewSignerFromKey(other)
	target.HostKey = string(ssh.MarshalAuthorizedKey(wrong.PublicKey()))
	if _, err = collectSSH(context.Background(), target, Credentials{Username: "monitor", Password: "secret"}); err == nil {
		t.Fatal("host-key substitution accepted")
	}
	if commands.Load() != 1 {
		t.Fatal("untrusted host received command")
	}
	listener.Close()
	<-done
}
