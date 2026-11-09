package srv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testMonitor(t *testing.T) *monitor {
	t.Helper()
	dir := t.TempDir()
	s := &Server{cfg: Config{DataDir: dir}}
	return newMonitor(s)
}

func TestPublishOnlyOnChange(t *testing.T) {
	m := testMonitor(t)
	snap := func(running int) *statusSnapshot {
		return &statusSnapshot{
			Counts:     statusCounts{Containers: 3, Running: running},
			Containers: []*containerView{{ID: "a", Name: "web"}},
			Images:     []imageView{},
		}
	}
	changed, cancel := m.subscribe()
	defer cancel()

	if !m.publish(snap(2)) {
		t.Fatal("first snapshot was not published")
	}
	if m.peek().Version != 1 {
		t.Fatalf("version = %d, want 1", m.peek().Version)
	}
	select {
	case <-changed:
	default:
		t.Fatal("subscriber was not told about the first snapshot")
	}

	if m.publish(snap(2)) {
		t.Fatal("identical content bumped the version")
	}
	select {
	case <-changed:
		t.Fatal("subscriber was notified without a change")
	default:
	}

	if !m.publish(snap(1)) || m.peek().Version != 2 {
		t.Fatal("changed content was not published as version 2")
	}

	raw, err := os.ReadFile(filepath.Join(m.s.cfg.DataDir, statusFileName))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk statusSnapshot
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Version != 2 || onDisk.Counts.Running != 1 {
		t.Fatalf("status.json = %+v", onDisk)
	}
	if _, err := os.Stat(filepath.Join(m.s.cfg.DataDir, statusFileName+".tmp")); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestInvalidateMarksDirty(t *testing.T) {
	m := testMonitor(t)
	m.dirty.Store(false)
	m.invalidate()
	if !m.dirty.Load() {
		t.Fatal("invalidate did not mark the snapshot dirty")
	}
}
