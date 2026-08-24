package main

import (
	"testing"
	"time"
)

func TestNotifyOfflineEnqueuesWithoutBlocking(t *testing.T) {
	m := &mailer{
		host: "127.0.0.1:1",
		from: "surge@example.com",
		jobs: make(chan notifyJob, 8),
	}
	start := time.Now()
	m.NotifyOffline("u1", "hello")
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("NotifyOffline blocked on deliver")
	}
	select {
	case job := <-m.jobs:
		if job.uid != "u1" || job.preview != "hello" {
			t.Fatalf("job=%+v", job)
		}
	default:
		t.Fatal("expected enqueue")
	}
}

func TestNotifyOfflineSkipsWhenDisabled(t *testing.T) {
	m := &mailer{jobs: make(chan notifyJob, 1)}
	m.NotifyOffline("u1", "hello")
	select {
	case <-m.jobs:
		t.Fatal("disabled mailer should not enqueue")
	default:
	}
}

func TestNotifyOfflineDropsWhenFull(t *testing.T) {
	m := &mailer{
		host: "127.0.0.1:1",
		from: "surge@example.com",
		jobs: make(chan notifyJob, 1),
	}
	m.jobs <- notifyJob{uid: "busy"}
	m.NotifyOffline("u2", "x")
	if len(m.jobs) != 1 {
		t.Fatalf("queue len=%d", len(m.jobs))
	}
}
