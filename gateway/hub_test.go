package main

import (
	"testing"

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
