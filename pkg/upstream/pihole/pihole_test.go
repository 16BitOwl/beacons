package pihole

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// toggleEntry
// ---------------------------------------------------------------------------

func TestToggleEntry_Add_AppendsMissingEntry(t *testing.T) {
	entries := []string{"a", "b"}
	got := toggleEntry(entries, "c", false)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[2] != "c" {
		t.Errorf("last entry = %q, want c", got[2])
	}
}

func TestToggleEntry_Add_DoesNotDuplicateExistingEntry(t *testing.T) {
	// toggleEntry always appends even if the entry already exists;
	// the caller's idempotency check prevents redundant PATCH calls.
	// Here we verify the returned slice length is correct.
	entries := []string{"a", "b", "c"}
	got := toggleEntry(entries, "b", false)
	// existing entries minus "b" → ["a", "c"], then append "b" → ["a", "c", "b"]
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestToggleEntry_Remove_DeletesMatchingEntry(t *testing.T) {
	entries := []string{"1.2.3.4 web", "5.6.7.8 api", "9.0.1.2 db"}
	got := toggleEntry(entries, "5.6.7.8 api", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, e := range got {
		if e == "5.6.7.8 api" {
			t.Errorf("removed entry should not be present")
		}
	}
}

func TestToggleEntry_Remove_NonExistentEntry_NoChange(t *testing.T) {
	entries := []string{"a", "b"}
	got := toggleEntry(entries, "c", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestToggleEntry_Remove_AllMatchingRemoved(t *testing.T) {
	// Only exact matches are removed — verify no over-removal.
	entries := []string{"web,target", "web,target,300", "api,target"}
	got := toggleEntry(entries, "web,target", true)
	// "web,target,300" is not an exact match — should remain.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; entries: %v", len(got), got)
	}
}

func TestToggleEntry_EmptyList_Add(t *testing.T) {
	got := toggleEntry(nil, "new", false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != "new" {
		t.Errorf("got[0] = %q, want new", got[0])
	}
}

func TestToggleEntry_EmptyList_Remove(t *testing.T) {
	got := toggleEntry(nil, "new", true)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestToggleEntry_PreservesOrder(t *testing.T) {
	entries := []string{"first", "second", "third"}
	got := toggleEntry(entries, "fourth", false)
	// first, second, third are preserved in order, fourth appended
	if got[0] != "first" || got[1] != "second" || got[2] != "third" || got[3] != "fourth" {
		t.Errorf("order not preserved: %v", got)
	}
}

// ---------------------------------------------------------------------------
// session.valid
// ---------------------------------------------------------------------------

func TestSessionValid_EmptySID_ReturnsFalse(t *testing.T) {
	s := session{sid: "", expiresAt: time.Now().Add(time.Hour)}
	if s.valid() {
		t.Error("session with empty SID should not be valid")
	}
}

func TestSessionValid_Expired_ReturnsFalse(t *testing.T) {
	s := session{sid: "tok", expiresAt: time.Now().Add(-time.Second)}
	if s.valid() {
		t.Error("expired session should not be valid")
	}
}

func TestSessionValid_ValidSession_ReturnsTrue(t *testing.T) {
	s := session{sid: "tok-abc", expiresAt: time.Now().Add(time.Hour)}
	if !s.valid() {
		t.Error("valid session should return true")
	}
}

func TestSessionValid_ExpiresAtExactlyNow_ReturnsFalse(t *testing.T) {
	// time.Now().Before(now) is false → invalid.
	s := session{sid: "tok", expiresAt: time.Now().Add(-time.Millisecond)}
	if s.valid() {
		t.Error("session expiring in the past should not be valid")
	}
}

func TestSessionValid_ZeroTime_ReturnsFalse(t *testing.T) {
	s := session{sid: "tok", expiresAt: time.Time{}}
	if s.valid() {
		t.Error("zero expiry time should not be valid")
	}
}
