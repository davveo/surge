package conv

import (
	"fmt"
	"strings"
)

const p2pPrefix = "p2p:"

// P2P returns the canonical 1:1 conversation id. Uids are ordered lexicographically
// so both sides resolve to the same cid.
func P2P(a, b string) (string, error) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return "", fmt.Errorf("conv: uid required")
	}
	if a == b {
		return "", fmt.Errorf("conv: cannot chat with self")
	}
	if a > b {
		a, b = b, a
	}
	return p2pPrefix + a + ":" + b, nil
}

// PeerUID returns the other participant of a p2p cid.
func PeerUID(cid, self string) (string, error) {
	left, right, err := splitP2P(cid)
	if err != nil {
		return "", err
	}
	switch self {
	case left:
		return right, nil
	case right:
		return left, nil
	default:
		return "", fmt.Errorf("conv: uid %s is not in %s", self, cid)
	}
}

// ResolveCID prefers an explicit cid, otherwise builds one from fromUID and peerUID.
func ResolveCID(fromUID, cid, peerUID string) (canonical string, peer string, err error) {
	fromUID = strings.TrimSpace(fromUID)
	cid = strings.TrimSpace(cid)
	peerUID = strings.TrimSpace(peerUID)
	if fromUID == "" {
		return "", "", fmt.Errorf("conv: from_uid required")
	}
	if cid != "" {
		peer, err = PeerUID(cid, fromUID)
		if err != nil {
			return "", "", err
		}
		if peerUID != "" && peerUID != peer {
			return "", "", fmt.Errorf("conv: peer_uid does not match cid")
		}
		return cid, peer, nil
	}
	canonical, err = P2P(fromUID, peerUID)
	if err != nil {
		return "", "", err
	}
	return canonical, peerUID, nil
}

func splitP2P(cid string) (string, string, error) {
	if !strings.HasPrefix(cid, p2pPrefix) {
		return "", "", fmt.Errorf("conv: not a p2p cid")
	}
	rest := strings.TrimPrefix(cid, p2pPrefix)
	left, right, ok := strings.Cut(rest, ":")
	if !ok || left == "" || right == "" || strings.Contains(right, ":") {
		return "", "", fmt.Errorf("conv: invalid p2p cid")
	}
	if left >= right {
		return "", "", fmt.Errorf("conv: p2p cid is not canonical")
	}
	return left, right, nil
}
