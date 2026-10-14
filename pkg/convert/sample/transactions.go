package sample

import "time"

// TransactionWindow is a transaction marked while recording: a request that
// started from Start through End belongs to it. A zero End leaves it open to
// the end of the recording.
type TransactionWindow struct {
	Name  string
	Start time.Time
	End   time.Time
}

// transactionAt returns the innermost transaction open when the exchange
// started: of the windows holding that time, the one that started last.
func transactionAt(windows []TransactionWindow, ex exchange) string {
	if len(windows) == 0 || !ex.HasStartedAt {
		return ""
	}
	name := ""
	var latest time.Time
	for _, window := range windows {
		if ex.StartedAt.Before(window.Start) || (!window.End.IsZero() && ex.StartedAt.After(window.End)) {
			continue
		}
		if name == "" || !window.Start.Before(latest) {
			name, latest = window.Name, window.Start
		}
	}
	return name
}
