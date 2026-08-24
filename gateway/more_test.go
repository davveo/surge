package main

import (
	"net/http"
	"testing"
)

func TestTooManyFailsClosedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("ENV", "")
	t.Setenv("SURGE_ENV", "")
	a := &httpAPI{}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	if !a.tooMany(req, "rl:test", 10, 0) {
		t.Fatal("production without redis must fail closed")
	}
}

func TestTooManyAllowsMemInDev(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("ENV", "")
	t.Setenv("SURGE_ENV", "")
	a := &httpAPI{limit: newMemLimiter()}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	if a.tooMany(req, "rl:dev", 2, 0) {
		t.Fatal("first hit should pass")
	}
}
