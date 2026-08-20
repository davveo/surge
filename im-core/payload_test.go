package main

import (
	"testing"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestPreviewOf(t *testing.T) {
	if previewOf(&imv1.Payload{Type: imv1.Payload_IMAGE}) != "[图片]" {
		t.Fatal("image")
	}
	if previewOf(&imv1.Payload{Type: imv1.Payload_FILE, Media: &imv1.Media{Filename: "a.pdf"}}) != "[文件] a.pdf" {
		t.Fatal("file")
	}
}

func TestValidateMedia(t *testing.T) {
	err := validateSend("u1", "c1", &imv1.Payload{Type: imv1.Payload_IMAGE})
	if err == nil {
		t.Fatal("expected object_key required")
	}
	if err := validateSend("u1", "c1", &imv1.Payload{Type: imv1.Payload_IMAGE, Media: &imv1.Media{ObjectKey: "u1/a.png"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPayloadBlobCompat(t *testing.T) {
	old := `{"objectKey":"u1/a.png","url":"http://x/a.png"}`
	p := payloadFromCols(int32(imv1.Payload_IMAGE), "hi", old, false)
	if p.Media == nil || p.Media.ObjectKey != "u1/a.png" {
		t.Fatalf("compat media %+v", p.Media)
	}
	p.QuoteText = "quoted"
	p.MentionUids = []string{"u2"}
	p.Link = &imv1.LinkPreview{Url: "https://example.com", Title: "ex"}
	raw := marshalPayloadBlob(p)
	p2 := payloadFromCols(int32(imv1.Payload_TEXT), "hi", raw, false)
	if p2.QuoteText != "quoted" || len(p2.MentionUids) != 1 || p2.Link == nil || p2.Link.Title != "ex" {
		t.Fatalf("roundtrip %+v", p2)
	}
}

func TestExtractMentions(t *testing.T) {
	got := extractMentions("hi @u2 and @u3")
	if len(got) != 2 || got[0] != "u2" {
		t.Fatalf("%v", got)
	}
}
