package conv

import "testing"

func TestP2PCanonical(t *testing.T) {
	a, err := P2P("u2", "u1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := P2P("u1", "u2")
	if err != nil {
		t.Fatal(err)
	}
	if a != "p2p:u1:u2" || a != b {
		t.Fatalf("got %q and %q", a, b)
	}
}

func TestP2PRejectsSelf(t *testing.T) {
	if _, err := P2P("u1", "u1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveCID(t *testing.T) {
	cid, peer, err := ResolveCID("u2", "", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if cid != "p2p:u1:u2" || peer != "u1" {
		t.Fatalf("cid=%s peer=%s", cid, peer)
	}
	cid2, peer2, err := ResolveCID("u2", cid, "")
	if err != nil {
		t.Fatal(err)
	}
	if cid2 != cid || peer2 != "u1" {
		t.Fatalf("cid=%s peer=%s", cid2, peer2)
	}
	cid3, peer3, err := ResolveCID("u2", "p2p:u1:u2", "u2")
	if err != nil {
		t.Fatal(err)
	}
	if cid3 != "p2p:u1:u2" || peer3 != "u1" {
		t.Fatalf("mismatch ignored cid=%s peer=%s", cid3, peer3)
	}
}

func TestPeerUIDRejectsStranger(t *testing.T) {
	if _, err := PeerUID("p2p:u1:u2", "u3"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveGroupCID(t *testing.T) {
	cid, peer, err := ResolveCID("u1", "grp:abc", "")
	if err != nil {
		t.Fatal(err)
	}
	if cid != "grp:abc" || peer != "" || Kind(cid) != KindGroup {
		t.Fatalf("cid=%s peer=%s", cid, peer)
	}
}
