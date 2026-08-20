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
	if got.GetSend().GetPayload().GetType() != imv1.Payload_TEXT {
		t.Fatalf("type %v", got.GetSend().GetPayload().GetType())
	}
}

func TestJSONLoneSurrogateBeforeMentionUids(t *testing.T) {
	// Browser JSON.stringify of a lone UTF-16 high surrogate, the exact
	// failure: protojson reads \ud83d then sees ","mentionUids".
	raw := []byte(`{"send":{"clientMsgId":"m1","cid":"p2p:u1:u2","payload":{"type":"TEXT","text":"\ud83d","mentionUids":["u2"]}}}`)
	got, err := Decode(false, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetSend().GetPayload().GetText() != "\uFFFD" {
		t.Fatalf("text %q", got.GetSend().GetPayload().GetText())
	}
	if len(got.GetSend().GetPayload().GetMentionUids()) != 1 {
		t.Fatalf("mentions %v", got.GetSend().GetPayload().GetMentionUids())
	}
	if got.GetSend().GetPayload().GetType() != imv1.Payload_TEXT {
		t.Fatalf("type %v", got.GetSend().GetPayload().GetType())
	}
}

func TestJSONPairedEmojiSurrogate(t *testing.T) {
	raw := []byte(`{"send":{"clientMsgId":"m1","payload":{"type":"TEXT","text":"\ud83d\ude00"}}}`)
	got, err := Decode(false, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetSend().GetPayload().GetText() != "😀" {
		t.Fatalf("text %q", got.GetSend().GetPayload().GetText())
	}
	if got.GetSend().GetPayload().GetType() != imv1.Payload_TEXT {
		t.Fatalf("type %v", got.GetSend().GetPayload().GetType())
	}
}

func TestJSONPayloadTypeAliases(t *testing.T) {
	cases := []struct {
		raw  string
		want imv1.Payload_Type
	}{
		{`{"send":{"clientMsgId":"m1","payload":{"type":"TEXT","text":"hi"}}}`, imv1.Payload_TEXT},
		{`{"send":{"clientMsgId":"m1","payload":{"type":"text","text":"hi"}}}`, imv1.Payload_TEXT},
		{`{"send":{"clientMsgId":"m1","payload":{"type":1,"text":"hi"}}}`, imv1.Payload_TEXT},
		{`{"send":{"clientMsgId":"m1","payload":{"type":"1","text":"hi"}}}`, imv1.Payload_TEXT},
		{`{"send":{"clientMsgId":"m1","payload":{"type":"IMAGE","media":{"objectKey":"a"}}}}`, imv1.Payload_IMAGE},
		{`{"send":{"clientMsgId":"m1","payload":{"text":"hi"}}}`, imv1.Payload_TEXT},
		{`{"send":{"clientMsgId":"m1","payload":{"type":"STICKER","stickerId":"x","media":{"objectKey":"sticker/x"}}}}`, imv1.Payload_IMAGE},
		{`{"send":{"clientMsgId":"m1","payload":{"type":"AUDIO","media":{"objectKey":"a"}}}}`, imv1.Payload_FILE},
		{`{"requestId":"5","send":{"clientMsgId":"m1","payload":{"type":"TEXT","text":"hi","mentionUids":[],"ephemeral":true}}}`, imv1.Payload_TEXT},
	}
	for _, tc := range cases {
		got, err := Decode(false, []byte(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if got.GetSend().GetPayload().GetType() != tc.want {
			t.Fatalf("%s: type %v want %v", tc.raw, got.GetSend().GetPayload().GetType(), tc.want)
		}
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
