package record

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// collectFollow runs Follow in the background and returns a channel of
// delivered records plus a cancel function.
func collectFollow(t *testing.T, path string) (<-chan Record, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Record, 16)
	done := make(chan error, 1)
	go func() {
		done <- Follow(ctx, path, 10*time.Millisecond, func(r Record) { out <- r })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Follow() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Follow() did not return after cancel")
		}
	})
	return out, cancel
}

func waitRecord(t *testing.T, ch <-chan Record) Record {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for followed record")
		return Record{}
	}
}

func TestFollowDeliversOnlyNewRecords(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Append(sampleRecord("pre-existing", 1)); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, FileName)
	out, _ := collectFollow(t, path)

	// Give the follower a moment to snapshot the current end of file.
	time.Sleep(50 * time.Millisecond)
	if err := store.Append(sampleRecord("new-1", 2)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(sampleRecord("new-2", 3)); err != nil {
		t.Fatal(err)
	}

	if got := waitRecord(t, out); got.Project != "new-1" {
		t.Errorf("first followed record project = %q, want new-1", got.Project)
	}
	if got := waitRecord(t, out); got.Project != "new-2" {
		t.Errorf("second followed record project = %q, want new-2", got.Project)
	}
	select {
	case r := <-out:
		t.Errorf("unexpected extra record delivered: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestFollowWaitsForFileCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	out, _ := collectFollow(t, path)

	time.Sleep(50 * time.Millisecond) // follower is polling a missing file
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Append(sampleRecord("born-later", 1)); err != nil {
		t.Fatal(err)
	}

	if got := waitRecord(t, out); got.Project != "born-later" {
		t.Errorf("followed record project = %q, want born-later", got.Project)
	}
}

func TestFollowNilCallback(t *testing.T) {
	if err := Follow(context.Background(), "x", time.Millisecond, nil); err == nil {
		t.Error("Follow(nil callback) succeeded, want error")
	}
}
