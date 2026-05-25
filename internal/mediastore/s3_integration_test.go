//go:build integration

// Integration test for the S3 backend. Spins up a real MinIO via testcontainers
// (same approach as internal/testdb's Postgres), so it needs Docker. Build-
// tagged so `go test ./...` without Docker doesn't fail. Run with:
//
//	go test -tags=integration ./internal/mediastore/...
package mediastore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// startMinIO brings up a MinIO container and returns its endpoint + credentials.
// Terminated via t.Cleanup.
func startMinIO(t *testing.T) (endpoint, access, secret string) {
	t.Helper()
	ctx := context.Background()
	const user, pass = "onscreentest", "onscreentestsecret"

	container, err := tcminio.Run(ctx, "minio/minio:RELEASE.2024-01-16T16-07-38Z",
		tcminio.WithUsername(user), tcminio.WithPassword(pass))
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("terminate minio: %v", err)
		}
	})

	ep, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return ep, container.Username, container.Password
}

func TestS3_Integration_AgainstMinIO(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainer)")
	}
	ctx := context.Background()
	endpoint, access, secret := startMinIO(t)

	const bucket = "media"
	const body = "hello-minio-from-onscreen"
	const filePath = "/srv/media/Movies/clip.mkv" // MediaRoot=/srv/media → key Movies/clip.mkv

	// Seed the bucket + object directly via minio-go (the backend under test only
	// reads).
	admin, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	if _, err := admin.PutObject(ctx, bucket, "Movies/clip.mkv",
		bytes.NewReader([]byte(body)), int64(len(body)),
		minio.PutObjectOptions{ContentType: "video/x-matroska"}); err != nil {
		t.Fatalf("put object: %v", err)
	}

	s, err := NewS3(S3Config{
		Endpoint:  endpoint,
		Bucket:    bucket,
		AccessKey: access,
		SecretKey: secret,
		UseSSL:    false,
		MediaRoot: "/srv/media",
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}

	t.Run("Ping", func(t *testing.T) {
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping: %v", err)
		}
	})

	t.Run("Stat", func(t *testing.T) {
		fi, err := s.Stat(ctx, filePath)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Size != int64(len(body)) {
			t.Errorf("Size = %d, want %d", fi.Size, len(body))
		}
		if fi.ModTime.IsZero() {
			t.Error("ModTime is zero")
		}
	})

	t.Run("Open_ReadsBytes", func(t *testing.T) {
		f, err := s.Open(ctx, filePath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if string(got) != body {
			t.Errorf("read %q, want %q", got, body)
		}
	})

	t.Run("Open_Seek_BacksHTTPRange", func(t *testing.T) {
		f, err := s.Open(ctx, filePath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer f.Close()
		if _, err := f.Seek(6, io.SeekStart); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		got, _ := io.ReadAll(f)
		if string(got) != body[6:] {
			t.Errorf("after seek read %q, want %q", got, body[6:])
		}
	})

	t.Run("SignedURL_PresignedFetchServesBytes", func(t *testing.T) {
		// The end-to-end offload path: a presigned URL a worker/CDN fetches
		// directly, off the app tier.
		u, err := s.SignedURL(ctx, filePath, 2*time.Minute)
		if err != nil {
			t.Fatalf("SignedURL: %v", err)
		}
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET presigned: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("presigned GET status = %d, want 200", resp.StatusCode)
		}
		got, _ := io.ReadAll(resp.Body)
		if string(got) != body {
			t.Errorf("presigned fetch body = %q, want %q", got, body)
		}
	})

	t.Run("Put_ThenReadBack", func(t *testing.T) {
		// Extracted cover art / pre-encoded segments are written back via Put.
		const pkey = "/srv/media/Movies/Dune (2021)/poster.jpg"
		want := []byte("resized-poster-bytes")
		if err := s.Put(ctx, pkey, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		fi, err := s.Stat(ctx, pkey)
		if err != nil {
			t.Fatalf("Stat after Put: %v", err)
		}
		if fi.Size != int64(len(want)) {
			t.Errorf("size = %d, want %d", fi.Size, len(want))
		}
		f, err := s.Open(ctx, pkey)
		if err != nil {
			t.Fatalf("Open after Put: %v", err)
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if string(got) != string(want) {
			t.Errorf("read %q, want %q", got, want)
		}
	})

	t.Run("Walk_ListsKeysRoundTrippedToFilePaths", func(t *testing.T) {
		// Seed a couple more objects so Walk has a tree to enumerate.
		for _, k := range []string{"TV/ShowA/s01e01.mkv", "TV/ShowA/s01e02.mkv"} {
			if _, err := admin.PutObject(ctx, bucket, k,
				bytes.NewReader([]byte("x")), 1, minio.PutObjectOptions{}); err != nil {
				t.Fatalf("seed %s: %v", k, err)
			}
		}

		seen := map[string]int64{}
		err := s.Walk(ctx, "/srv/media/TV", func(o ObjectInfo) error {
			seen[o.Key] = o.Size
			return nil
		})
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		// Keys must come back FilePath-shaped (MediaRoot prepended), so they can
		// be passed straight to Stat/Open.
		for _, want := range []string{"/srv/media/TV/ShowA/s01e01.mkv", "/srv/media/TV/ShowA/s01e02.mkv"} {
			if _, ok := seen[want]; !ok {
				t.Errorf("Walk missing key %q; got %v", want, seen)
			}
		}
		// And a yielded key really is usable by Stat.
		if _, err := s.Stat(ctx, "/srv/media/TV/ShowA/s01e01.mkv"); err != nil {
			t.Errorf("Stat on a walked key failed: %v", err)
		}
	})

	t.Run("Stat_MissingIsErrNotFound", func(t *testing.T) {
		if _, err := s.Stat(ctx, "/srv/media/nope/missing.mkv"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing Stat: got %v, want ErrNotFound", err)
		}
	})

	t.Run("RealNotFoundMapsToErrNotFound", func(t *testing.T) {
		// Validate mapS3Err against a *genuine* MinIO not-found response — the unit
		// tests can only use synthetic errors. GetObject defers the error to the
		// first read, so Open then read.
		f, err := s.Open(ctx, "/srv/media/nope/missing.mkv")
		if err == nil {
			_, err = io.ReadAll(f)
			_ = f.Close()
		}
		if err == nil {
			t.Fatal("expected an error reading a missing object")
		}
		if !errors.Is(mapS3Err(err), ErrNotFound) {
			t.Errorf("mapS3Err(%v) did not map to ErrNotFound", err)
		}
	})

	t.Run("Ping_NonexistentBucketFails", func(t *testing.T) {
		// The admin "Test connection" negative path: valid creds, wrong bucket.
		bad, err := NewS3(S3Config{
			Endpoint: endpoint, Bucket: "does-not-exist",
			AccessKey: access, SecretKey: secret, UseSSL: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := bad.Ping(ctx); err == nil {
			t.Error("Ping on a missing bucket should fail")
		}
	})
}
