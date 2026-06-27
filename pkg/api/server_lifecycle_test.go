package api

import "testing"

func TestNewServerDoesNotStartReplayRetentionJanitors(t *testing.T) {
	server := NewServer(ServerConfig{})
	if browserRunning, scriptRunning := replayRetentionJanitorsRunning(server); browserRunning || scriptRunning {
		t.Fatalf("NewServer started replay janitors = browser:%v script:%v, want both false", browserRunning, scriptRunning)
	}

	server.startReplayRetentionJanitors()
	t.Cleanup(func() {
		if server.browserReplay != nil {
			server.browserReplay.Close()
		}
		if server.scriptDebug != nil {
			server.scriptDebug.Close()
		}
	})

	if browserRunning, scriptRunning := replayRetentionJanitorsRunning(server); !browserRunning || !scriptRunning {
		t.Fatalf("startReplayRetentionJanitors started janitors = browser:%v script:%v, want both true", browserRunning, scriptRunning)
	}
}

func replayRetentionJanitorsRunning(server *Server) (bool, bool) {
	browserRunning := false
	if server != nil && server.browserReplay != nil {
		server.browserReplay.mu.RLock()
		browserRunning = server.browserReplay.janitorCancel != nil
		server.browserReplay.mu.RUnlock()
	}
	scriptRunning := false
	if server != nil && server.scriptDebug != nil {
		server.scriptDebug.mu.RLock()
		scriptRunning = server.scriptDebug.janitorCancel != nil
		server.scriptDebug.mu.RUnlock()
	}
	return browserRunning, scriptRunning
}
