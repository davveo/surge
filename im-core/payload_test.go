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
