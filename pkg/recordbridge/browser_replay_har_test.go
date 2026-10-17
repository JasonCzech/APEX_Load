package recordbridge

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func publishFakeReplayHAR(t *testing.T, dir, name, page string) string {
	t.Helper()
	path := filepath.Join(dir, "apexload_browser_"+name+"_scrubbed.har")
	body := `{"log":{"version":"1.2","creator":{"name":"apexload","version":"test"},"pages":[{"id":"` + page + `"}],"entries":[]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestReplayHARStoreKeepsOnlyFencedCopies(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	harDir := t.TempDir()
	original := publishFakeReplayHAR(t, harDir, "unfenced", "unfenced")
	record := &BrowserReplaySession{ID: "unfenced"}
	if copied := manager.retainReplayHAR(record, []protocol.BrowserHARArtifact{{Path: original}}, false); copied != "" {
		t.Fatalf("an unfenced record kept %s", copied)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("the original was not retired: %v", err)
	}
	original = publishFakeReplayHAR(t, harDir, "kept", "kept")
	copied := manager.retainReplayHAR(record, []protocol.BrowserHARArtifact{{Path: original}}, true)
	if copied == "" || !strings.HasPrefix(copied, filepath.Join(os.TempDir(), replayHARRootName)) {
		t.Fatalf("copy %q outside the managed root", copied)
	}
	manager.Close()
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("close kept the copy: %v", err)
	}
	if _, err := manager.har.write(record, strings.NewReader("{}"), 2); !errors.Is(err, errReplayHARStoreClosed) {
		t.Fatalf("a closed store accepted a write: %v", err)
	}
	// A copy that cannot be kept still retires the original.
	original = publishFakeReplayHAR(t, harDir, "uncopied", "uncopied")
	if copied := manager.retainReplayHAR(record, []protocol.BrowserHARArtifact{{Path: original}}, true); copied != "" {
		t.Fatalf("a closed store kept %s", copied)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("the original stayed after a failed copy: %v", err)
	}
}
func TestReplayHARStoreManagedRoot(t *testing.T) {
	tmp := t.TempDir()
	// os.TempDir reads TMPDIR on Unix and TMP (then TEMP) on Windows.
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	root := filepath.Join(tmp, replayHARRootName)
	makeReplayHARRootPrivate(t, root)
	if err := os.MkdirAll(filepath.Join(root, "manager-ended"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * replayHARStaleAfter)
	if err := os.Chtimes(filepath.Join(root, "manager-ended"), old, old); err != nil {
		t.Fatal(err)
	}
	var store replayHARStore
	copied, err := store.write(&BrowserReplaySession{ID: "a"}, strings.NewReader("{}"), 2)
	if err != nil || filepath.Dir(filepath.Dir(copied)) != root {
		t.Fatalf("copy %q under %q: %v", copied, root, err)
	}
	if _, err := os.Stat(filepath.Join(root, "manager-ended")); !os.IsNotExist(err) {
		t.Fatalf("an ended process's directory stayed: %v", err)
	}
	store.close()

	// The root is a link elsewhere: nothing there is touched.
	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "old")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, root); err != nil {
		t.Fatal(err)
	}
	var linked replayHARStore
	copied, err = linked.write(&BrowserReplaySession{ID: "b"}, strings.NewReader("{}"), 2)
	if err != nil || strings.HasPrefix(copied, root+string(os.PathSeparator)) || strings.HasPrefix(copied, elsewhere) {
		t.Fatalf("copy %q through a linked root: %v", copied, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the sweep followed the link: %v", err)
	}
	linked.close()

	// A root that is not private (another mode) is not adopted either.
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	makeReplayHARRootPrivate(t, root)
	makeReplayHARRootShared(t, root)
	var shared replayHARStore
	copied, err = shared.write(&BrowserReplaySession{ID: "c"}, strings.NewReader("{}"), 2)
	if err != nil || strings.HasPrefix(copied, root+string(os.PathSeparator)) {
		t.Fatalf("copy %q under a shared root: %v", copied, err)
	}
	shared.close()

	// A live manager keeps its directory fresh, and makes it again if another
	// process removed it.
	makeReplayHARRootPrivateAgain(t, root)
	var live replayHARStore
	first, err := live.write(&BrowserReplaySession{ID: "d"}, strings.NewReader("{}"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Dir(first), old, old); err != nil {
		t.Fatal(err)
	}
	live.sweep(func(*BrowserReplaySession) bool { return true })
	if info, err := os.Stat(filepath.Dir(first)); err != nil || time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("the sweep did not keep the live directory fresh: %v %v", info, err)
	}
	if err := os.RemoveAll(filepath.Dir(first)); err != nil {
		t.Fatal(err)
	}
	if second, err := live.write(&BrowserReplaySession{ID: "e"}, strings.NewReader("{}"), 2); err != nil || second == "" {
		t.Fatalf("a removed live directory was not made again: %q %v", second, err)
	}
	live.close()
}
func TestReplayHARStoreCopiesOutsideTheLock(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	t.Cleanup(manager.Close)
	record := &BrowserReplaySession{ID: "slow"}
	write := func() (*blockingReader, chan error, chan string) {
		reader := &blockingReader{started: make(chan struct{}), release: make(chan struct{}), data: strings.NewReader("{}")}
		errs, paths := make(chan error, 1), make(chan string, 1)
		t.Cleanup(func() {
			select {
			case <-reader.release:
			default:
				close(reader.release)
			}
		})
		go func() {
			path, err := manager.har.write(record, reader, 2)
			errs <- err
			paths <- path
		}()
		select {
		case <-reader.started:
		case <-time.After(5 * time.Second):
			t.Fatal("copy did not start")
		}
		return reader, errs, paths
	}
	reader, errs, paths := write()
	swept := make(chan struct{})
	go func() {
		manager.har.sweep(func(*BrowserReplaySession) bool { return true })
		close(swept)
	}()
	select {
	case <-swept:
	case <-time.After(5 * time.Second):
		t.Fatal("a sweep waited for a copy in progress")
	}
	close(reader.release)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if path := <-paths; path == "" {
		t.Fatal("the copy was not kept")
	}

	reader, errs, paths = write()
	manager.har.close()
	close(reader.release)
	if err := <-errs; !errors.Is(err, errReplayHARStoreClosed) {
		t.Fatalf("a copy finished after close: %v", err)
	}
	if path := <-paths; path != "" {
		t.Fatalf("a closed store kept %s", path)
	}
}

// blockingReader yields its data only after release is closed.
type blockingReader struct {
	started chan struct{}
	release chan struct{}
	data    io.Reader
	once    sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.data.Read(p)
}

func TestReplayHARUnfencedRecordRetiresCopyBeforeIDReuse(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	t.Cleanup(manager.Close)
	session := &BrowserReplaySession{ID: "reused", Status: BrowserReplayCompleted, workerFinished: true, workerRecordedTerminal: true}
	original := publishFakeReplayHAR(t, t.TempDir(), "lost-fence", "first-attempt")
	copied := manager.retainReplayHAR(session, []protocol.BrowserHARArtifact{{Path: original}}, true)
	if copied == "" {
		t.Fatal("native copy was not retained")
	}
	session.harCopy = copied
	manager.sessions[session.ID] = session
	manager.retireUnfencedSession(session)
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("unfenced copy remains: %v", err)
	}
	if _, ok := manager.Get(session.ID); ok {
		t.Fatal("unfenced record remains addressable")
	}
	replacement, err := manager.Start("", portReplayScript(t), BrowserReplayStartRequest{ReplaySessionID: session.ID, StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	if har, available, err := manager.ReplayHARFor(replacement); available || err != nil {
		har.Close()
		t.Fatalf("replacement inherited old HAR: %v %v", available, err)
	}
}
