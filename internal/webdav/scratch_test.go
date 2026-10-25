package webdav

import (
	"bytes"
	"errors"
	"testing"
)

func TestScratchAreaBoundsEachPersonAndTheServer(t *testing.T) {
	a := newScratchArea()
	big := bytes.Repeat([]byte("x"), scratchMaxBytes)
	// One person fills their share; others still have room.
	for i := range scratchOwnerBytes / scratchMaxBytes {
		if _, err := a.put("lib/~$"+string(rune('a'+i)), "mallory", big); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := a.put("lib/~$more", "mallory", []byte("x")); !errors.Is(err, errScratchFull) {
		t.Fatalf("over a person's bytes: %v", err)
	}
	if _, err := a.put("lib/~$alice", "alice", []byte("owner")); err != nil {
		t.Fatalf("another person is crowded out: %v", err)
	}
	// Replacing a file counts only the difference; taking over another's moves it.
	if created, err := a.put("lib/~$a", "mallory", []byte("small")); err != nil || created {
		t.Fatalf("replace: %v %v", created, err)
	}
	if _, err := a.put("lib/~$more", "mallory", []byte("x")); err != nil {
		t.Fatalf("room freed by a smaller file: %v", err)
	}
	if _, err := a.put("lib/~$b", "alice", []byte("mine now")); err != nil || a.owners["alice"].files != 2 || a.owners["mallory"].files != scratchOwnerBytes/scratchMaxBytes {
		t.Fatalf("takeover: %v %+v %+v", err, a.owners["alice"], a.owners["mallory"])
	}
	for key := range a.files {
		a.delete(key)
	}
	if a.bytes != 0 || len(a.owners) != 0 {
		t.Fatalf("deleting left counts: %d %v", a.bytes, a.owners)
	}

	// Many people together stop at the server's total.
	n := 0
	for ; n <= scratchMaxTotal/scratchMaxBytes; n++ {
		if _, err := a.put("lib/~$"+string(rune(0x100+n)), string(rune(0x100+n)), big); err != nil {
			if !errors.Is(err, errScratchFull) {
				t.Fatal(err)
			}
			break
		}
	}
	if n != scratchMaxTotal/scratchMaxBytes || a.bytes > scratchMaxTotal {
		t.Fatalf("server total: %d files, %d bytes", n, a.bytes)
	}
}
