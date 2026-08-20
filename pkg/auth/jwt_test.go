package auth

import "testing"

func TestIssueParse(t *testing.T) {
	tok, err := Issue("secret", "u1", "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse("secret", tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "u1" || c.DeviceID != "web" {
		t.Fatalf("%+v", c)
	}
	if _, err := Parse("other", tok); err == nil {
		t.Fatal("expected mismatch")
	}
}
