package main

import (
	"testing"
	"time"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestHubPushFallsBackToUID(t *testing.T) {
	h := newHub(nil, "gw")
	c := &Conn{id: "live", uid: "u2", send: make(chan *imv1.Envelope, 1), hub: h}
	h.byConn[c.id] = c
	h.byUID["u2"] = map[string]*Conn{c.id: c}

	h.push(&imv1.GatewayPush{
		Uid:    "u2",
		ConnId: "stale-conn",
		Push:   &imv1.Push{Cid: "sys:roster", FromUid: "u1"},
	})
	select {
	case env := <-c.send:
		if env.GetPush() == nil || env.GetPush().GetCid() != "sys:roster" {
			t.Fatalf("got %+v", env)
		}
	default:
		t.Fatal("stale conn id should fall back to live uid connections")
	}
}

func TestHubListDevicesMarksSelf(t *testing.T) {
	h := newHub(nil, "gw")
	mine := &Conn{id: "c1", uid: "u1", deviceID: "web-a", hub: h}
	other := &Conn{id: "c2", uid: "u1", deviceID: "web-b", hub: h}
	h.byConn[mine.id] = mine
	h.byConn[other.id] = other
	h.byUID["u1"] = map[string]*Conn{mine.id: mine, other.id: other}

	list := h.listDevices("u1", "web-a")
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}
	got := map[string]string{}
	for _, d := range list {
		got[d["device_id"]] = d["self"]
	}
	if got["web-a"] != "1" || got["web-b"] != "0" {
		t.Fatalf("self flags %+v", got)
	}
	if !h.isSelfDevice("c1", "web-a") {
		t.Fatal("c1 should be current device")
	}
	if h.isSelfDevice("c2", "web-a") {
		t.Fatal("c2 should not be current device")
	}
}

func TestHubPushRosterSkipsSelf(t *testing.T) {
	h := newHub(nil, "gw")
	c := &Conn{id: "me", uid: "u1", send: make(chan *imv1.Envelope, 1), hub: h}
	h.byConn[c.id] = c
	h.byUID["u1"] = map[string]*Conn{c.id: c}
	h.pushRoster("u1", "u1", "group_invite", "grp:x")
	select {
	case <-c.send:
		t.Fatal("should not push roster to self")
	default:
	}
}

func TestEnqueueDropsTypingWhenFull(t *testing.T) {
	c := &Conn{id: "c", send: make(chan *imv1.Envelope, 1)}
	c.send <- &imv1.Envelope{}
	c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Typing{Typing: &imv1.Typing{}}})
	if len(c.send) != 1 {
		t.Fatal("typing should drop when queue is full")
	}
}

func TestEnqueueWaitsForPush(t *testing.T) {
	old := sendQueueWait
	sendQueueWait = 300 * time.Millisecond
	defer func() { sendQueueWait = old }()
	c := &Conn{id: "c", send: make(chan *imv1.Envelope, 1)}
	c.send <- &imv1.Envelope{}
	go func() {
		time.Sleep(40 * time.Millisecond)
		<-c.send
	}()
	c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Push{Push: &imv1.Push{Cid: "x"}}})
	select {
	case env := <-c.send:
		if env.GetPush() == nil {
			t.Fatal("want queued push")
		}
	default:
		t.Fatal("push should wait and enqueue")
	}
}

func TestEnqueueStallsWithoutEnqueue(t *testing.T) {
	old := sendQueueWait
	sendQueueWait = 20 * time.Millisecond
	defer func() { sendQueueWait = old }()
	c := &Conn{id: "c", send: make(chan *imv1.Envelope, 1)}
	c.send <- &imv1.Envelope{}
	c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Push{Push: &imv1.Push{Cid: "x"}}})
	if len(c.send) != 1 {
		t.Fatal("stalled push should not replace the queued frame")
	}
}
