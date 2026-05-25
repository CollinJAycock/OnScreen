package mediastore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible backend (AWS S3, MinIO, Backblaze B2,
// Wasabi, Cloudflare R2 — anything speaking the S3 API). Endpoint is the host
// without a scheme (e.g. "s3.us-west-2.amazonaws.com"); UseSSL picks http vs
// https.
//
// Key mapping: a caller's key is a local absolute FilePath (the scanner's
// existing identifier). MediaRoot is stripped from its front and PathPrefix is
// prepended to form the object key — so /mnt/c/media/Movies/x.mkv with
// MediaRoot=/mnt/c/media and PathPrefix=library/ becomes library/Movies/x.mkv.
// This is the interim content-addressing scheme until a stable per-file key
// lands (HA roadmap, "Content addressing").
type S3Config struct {
	Endpoint   string
	Region     string
	Bucket     string
	AccessKey  string
	SecretKey  string
	UseSSL     bool
	MediaRoot  string
	PathPrefix string
	CDNBaseURL string
}

// S3 is an S3-compatible Store. Safe for concurrent use (minio.Client is).
type S3 struct {
	client     *minio.Client
	bucket     string
	mediaRoot  string
	pathPrefix string
	cdnBase    string
}

// compile-time assertions that S3 satisfies Store and Lister.
var (
	_ Store  = (*S3)(nil)
	_ Lister = (*S3)(nil)
)

// NewS3 builds an S3 backend. It does not perform any network I/O — call Ping to
// verify connectivity / credentials (the admin "Test connection" path).
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("mediastore: s3 endpoint and bucket are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("mediastore: new s3 client: %w", err)
	}
	return &S3{
		client:     client,
		bucket:     cfg.Bucket,
		mediaRoot:  cfg.MediaRoot,
		pathPrefix: cfg.PathPrefix,
		cdnBase:    strings.TrimRight(cfg.CDNBaseURL, "/"),
	}, nil
}

// objectKey maps a local FilePath to a bucket object key: strip MediaRoot,
// normalise to forward slashes, prepend PathPrefix. Exported behaviour is
// covered by tests so the mapping can't drift silently.
func (s *S3) objectKey(filePath string) string {
	p := filepath.ToSlash(filePath)
	if s.mediaRoot != "" {
		root := strings.TrimRight(filepath.ToSlash(s.mediaRoot), "/") + "/"
		p = strings.TrimPrefix(p, root)
	}
	p = strings.TrimLeft(p, "/")
	if s.pathPrefix != "" {
		p = path.Join(strings.Trim(s.pathPrefix, "/"), p)
	}
	return p
}

// filePath is the inverse of objectKey: it reconstructs a caller-facing key
// (FilePath-shaped) from a bucket object key, so Walk yields keys Open/Stat
// accept. objectKey(filePath(k)) == k for keys produced by objectKey.
func (s *S3) filePath(objKey string) string {
	p := objKey
	if s.pathPrefix != "" {
		p = strings.TrimPrefix(p, strings.Trim(s.pathPrefix, "/")+"/")
	}
	if s.mediaRoot != "" {
		return strings.TrimRight(filepath.ToSlash(s.mediaRoot), "/") + "/" + p
	}
	return "/" + p
}

// Walk implements Lister via S3 ListObjects under the mapped prefix. Yielded keys
// are mapped back to the FilePath namespace so they can be passed to Open/Stat.
func (s *S3) Walk(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	objPrefix := s.objectKey(prefix)
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    objPrefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return fmt.Errorf("mediastore: list: %w", obj.Err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(ObjectInfo{
			Key:     s.filePath(obj.Key),
			Size:    obj.Size,
			ModTime: obj.LastModified,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Open implements Store. The returned *minio.Object is range-seekable; not-found
// surfaces lazily on first read, but the serving path Stats before Open so the
// 404 is caught up front.
func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, s.objectKey(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, mapS3Err(err)
	}
	return obj, nil
}

// Stat implements Store.
func (s *S3) Stat(ctx context.Context, key string) (FileInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, s.objectKey(key), minio.StatObjectOptions{})
	if err != nil {
		return FileInfo{}, mapS3Err(err)
	}
	return FileInfo{Size: info.Size, ModTime: info.LastModified}, nil
}

// SignedURL implements Store. When a CDN base is configured the object is served
// through the CDN (cdnBase/objectKey) — the deployment is expected to front a
// public-read bucket or give the CDN origin credentials. Otherwise it returns a
// time-limited presigned GET URL valid against the bucket directly (works with
// private buckets).
func (s *S3) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	objKey := s.objectKey(key)
	if s.cdnBase != "" {
		return s.cdnBase + "/" + objKey, nil
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, objKey, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("mediastore: presign: %w", err)
	}
	return u.String(), nil
}

// Ping verifies the bucket is reachable with the configured credentials — the
// admin "Test connection" check. BucketExists exercises auth + network without
// needing any object to be present.
func (s *S3) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("mediastore: bucket check: %w", err)
	}
	if !ok {
		return fmt.Errorf("mediastore: bucket %q not found or not accessible", s.bucket)
	}
	return nil
}

// mapS3Err converts a not-found S3 response to the package's ErrNotFound so
// handlers 404 instead of 500; other errors pass through.
func mapS3Err(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == http.StatusNotFound || resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" {
		return ErrNotFound
	}
	return err
}
