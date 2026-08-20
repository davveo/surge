package main

import (
	"context"
	"strings"
	"testing"
)

func TestPresignUsesPublicHostWithoutDialingIt(t *testing.T) {
	m, err := newMediaStore(config{
		MinioEndpoint:  "minio:9000",
		MinioAccess:    "surge",
		MinioSecret:    "surge-minio",
		MinioBucket:    "surge",
		MinioPublicURL: "http://127.0.0.1:9001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.signer == m.cli {
		t.Fatal("expected a separate public-host signer")
	}
	// Skip BucketExists against the internal hostname.
	m.once.Do(func() {})

	out, err := m.presign(context.Background(), "u1", "a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.PutURL, "http://127.0.0.1:9001/surge/") {
		t.Fatalf("put_url want public host, got %s", out.PutURL)
	}
	if strings.Contains(out.PutURL, "minio:9000") {
		t.Fatalf("put_url leaked internal endpoint: %s", out.PutURL)
	}
	if out.GetURL != "http://127.0.0.1:9001/surge/"+out.ObjectKey {
		t.Fatalf("get_url %s", out.GetURL)
	}
}

func TestLocalEndpointSharesSigner(t *testing.T) {
	m, err := newMediaStore(config{
		MinioEndpoint:  "127.0.0.1:9001",
		MinioAccess:    "surge",
		MinioSecret:    "surge-minio",
		MinioBucket:    "surge",
		MinioPublicURL: "http://127.0.0.1:9001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.signer != m.cli {
		t.Fatal("local go run should share one client")
	}
}
