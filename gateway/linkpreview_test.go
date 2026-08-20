package main

import (
	"net/url"
	"testing"
)

func TestAssertPublicURL(t *testing.T) {
	cases := []string{"http://127.0.0.1/", "http://localhost/x", "http://10.0.0.1/", "ftp://example.com"}
	for _, c := range cases {
		u, err := url.Parse(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := assertPublicURL(u); err == nil {
			t.Fatalf("expected block %s", c)
		}
	}
	u, _ := url.Parse("https://example.com/a")
	if err := assertPublicURL(u); err != nil {
		t.Fatal(err)
	}
}
