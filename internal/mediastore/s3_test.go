package mediastore

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestS3_ObjectKey_MapsLocalPathToBucketKey(t *testing.T) {
	cases := []struct {
		name       string
		mediaRoot  string
		pathPrefix string
		filePath   string
		want       string
	}{
		{
			name:      "strips media root",
			mediaRoot: "/mnt/c/media",
			filePath:  "/mnt/c/media/Movies/Dune.mkv",
			want:      "Movies/Dune.mkv",
		},
		{
			name:       "strips root and prepends prefix",
			mediaRoot:  "/mnt/c/media",
			pathPrefix: "library/",
			filePath:   "/mnt/c/media/Movies/Dune.mkv",
			want:       "library/Movies/Dune.mkv",
		},
		{
			name:     "no root → leading slash trimmed",
			filePath: "/Movies/Dune.mkv",
			want:     "Movies/Dune.mkv",
		},
		{
			name:      "windows backslashes normalised to forward slashes",
			mediaRoot: "C:/media",
			filePath:  `C:\media\TV\Show\ep.mkv`,
			want:      "TV/Show/ep.mkv",
		},
		{
			name:       "prefix slashes are not doubled",
			mediaRoot:  "/m",
			pathPrefix: "/p/",
			filePath:   "/m/a/b.mkv",
			want:       "p/a/b.mkv",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &S3{mediaRoot: tc.mediaRoot, pathPrefix: tc.pathPrefix}
			if got := s.objectKey(tc.filePath); got != tc.want {
				t.Errorf("objectKey(%q) = %q, want %q", tc.filePath, got, tc.want)
			}
		})
	}
}

func TestNewS3_RequiresEndpointAndBucket(t *testing.T) {
	if _, err := NewS3(S3Config{Bucket: "b"}); err == nil {
		t.Error("missing endpoint: expected error")
	}
	if _, err := NewS3(S3Config{Endpoint: "s3.example.com"}); err == nil {
		t.Error("missing bucket: expected error")
	}
	if _, err := NewS3(S3Config{Endpoint: "s3.example.com", Bucket: "b"}); err != nil {
		t.Errorf("valid config: unexpected error %v", err)
	}
}

func TestS3_SignedURL_CDNBaseBypassesPresigning(t *testing.T) {
	// With a CDN base, SignedURL returns cdnBase/objectKey directly (no network,
	// no signature) — the bytes are served through the CDN.
	s, err := NewS3(S3Config{
		Endpoint:   "s3.example.com",
		Bucket:     "media",
		MediaRoot:  "/mnt/media",
		PathPrefix: "lib/",
		CDNBaseURL: "https://cdn.example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SignedURL(context.Background(), "/mnt/media/Movies/x.mkv", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	want := "https://cdn.example.com/lib/Movies/x.mkv"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestS3_SignedURL_PresignsWhenNoCDN(t *testing.T) {
	// Presigning is a local signature computation (no round-trip), so this runs
	// offline. The URL must carry the endpoint, the object key, and an AWS
	// signature query parameter.
	s, err := NewS3(S3Config{
		Endpoint:  "s3.example.com",
		Region:    "us-east-1",
		Bucket:    "media",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "secret",
		MediaRoot: "/mnt/media",
		UseSSL:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SignedURL(context.Background(), "/mnt/media/Movies/x.mkv", 10*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	for _, want := range []string{"s3.example.com", "media", "Movies/x.mkv", "X-Amz-Signature"} {
		if !strings.Contains(got, want) {
			t.Errorf("presigned URL %q missing %q", got, want)
		}
	}
}
