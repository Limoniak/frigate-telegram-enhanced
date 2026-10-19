package frigate

import "testing"

func TestEventSnapshotHeightPath(t *testing.T) {
	if got, want := EventSnapshotHeightPath("a b", 720), "/api/events/a%20b/snapshot.jpg?bbox=1&h=720"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
