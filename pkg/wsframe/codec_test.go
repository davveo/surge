package wsframe

import (
	"testing"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestJSONRoundTrip(t *testing.T) {
	env := &imv1.Envelope{
		RequestId: 1,
		Body: &imv1.Envelope_Send{Send: &imv1.SendRequest{
			ClientMsgId: "m1",
			PeerUid:     "u2",
			Payload:     &imv1.Payload{Type: imv1.Payload_TEXT, Text: "hi"},
		}},
	}
	raw, err := Encode(false, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(false, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetSend().GetPayload().GetText() != "hi" {
		t.Fatalf("payload %v", got.GetSend().GetPayload())
	}
}

func TestBinaryRoundTrip(t *testing.T) {
	env := &imv1.Envelope{Body: &imv1.Envelope_Heartbeat{Heartbeat: &imv1.Heartbeat{TsMs: 7}}}
	raw, err := Encode(true, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(true, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetHeartbeat().GetTsMs() != 7 {
		t.Fatalf("%v", got)
	}
}
