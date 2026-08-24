package main

import (
	"context"
	"testing"
)

func TestSeqKeysUniqueSorted(t *testing.T) {
	keys := seqKeys("grp:1", []string{"u2", "u1", "u2", ""})
	want := []string{convSeqKey("grp:1"), syncSeqKey("u1"), syncSeqKey("u2")}
	if len(keys) != len(want) {
		t.Fatalf("len=%d want %d: %v", len(keys), len(want), keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys=%v want %v", keys, want)
		}
	}
	again := uniqueSorted([]string{"b", "a", "a", "", "b"})
	if len(again) != 2 || again[0] != "a" || again[1] != "b" {
		t.Fatalf("uniqueSorted=%v", again)
	}
}

func TestMemSeqMonotonic(t *testing.T) {
	s := newMemSeq()
	a, _ := s.Next(context.Background(), "seq:conv:x")
	b, _ := s.Next(context.Background(), "seq:conv:x")
	if a != 1 || b != 2 {
		t.Fatalf("got %d %d", a, b)
	}
}
