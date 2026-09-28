package api

import (
	"context"
	"testing"
	"time"

	redisserver "github.com/alicebob/miniredis/v2/server"
)

// Exercise an established TCP connection whose server stops replying after
// accepting a command. The cache's socket timeout is deliberately much longer
// than the operation deadline, as in metrics writers during a dependency stall.
func TestRedisCacheOperationsRespectCallerDeadline(t *testing.T) {
	for _, backend := range []string{cacheBackendRedis, cacheBackendValkey} {
		for _, operation := range []string{"read", "write"} {
			t.Run(backend+"/"+operation, func(t *testing.T) {
				server, err := redisserver.NewServer("127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				release := make(chan struct{})
				entered := make(chan struct{}, 1)
				server.SetPreHook(func(peer *redisserver.Peer, command string, args ...string) bool {
					switch command {
					case "HELLO":
						peer.WriteMapLen(0)
					case "PING":
						peer.WriteInline("PONG")
					case "GET", "SET":
						select {
						case entered <- struct{}{}:
						default:
						}
						<-release
						peer.WriteError("ERR deliberately delayed response")
					default:
						peer.WriteOK()
					}
					return true
				})
				cache, err := newRedisCacheBackend(cacheRuntimeConfig{Backend: backend, RedisURL: "redis://" + server.Addr().String(), ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second})
				if err != nil {
					close(release)
					server.Close()
					t.Fatal(err)
				}
				defer func() { close(release); _ = cache.Close(); server.Close() }()
				warmCtx, warmCancel := context.WithTimeout(context.Background(), 2*time.Second)
				err = cache.Ping(warmCtx)
				warmCancel()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				result := make(chan error, 1)
				go func() {
					if operation == "write" {
						result <- cache.SetRunSnapshot(ctx, RunLiveStateSnapshot{TestID: "run"}, time.Minute)
					} else {
						_, err := cache.GetRunSnapshot(ctx, "run")
						result <- err
					}
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("command did not reach established connection")
				}
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("stalled command succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("cache command exceeded caller deadline and remained blocked on socket timeout")
				}
			})
		}
	}
}
