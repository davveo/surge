package mail

import "testing"

func TestResolveAddr(t *testing.T) {
	if got := ResolveAddr("smtp.example.com", "", false); got != "smtp.example.com:25" {
		t.Fatalf("plain=%s", got)
	}
	if got := ResolveAddr("smtp.example.com", "user", false); got != "smtp.example.com:587" {
		t.Fatalf("auth=%s", got)
	}
	if got := ResolveAddr("smtp.example.com:2525", "user", true); got != "smtp.example.com:2525" {
		t.Fatalf("explicit=%s", got)
	}
}
