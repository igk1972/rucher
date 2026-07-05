package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 syncs the fleet store from an S3-compatible bucket into a local checkout.
type S3 struct {
	Endpoint  string // host:port, no scheme
	Bucket    string
	Prefix    string // key prefix within the bucket ("" = bucket root)
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
	CachePath string
}

// objInfo is the minimal per-object identity used to compute a store revision.
type objInfo struct{ Key, ETag string }

// resolveDest joins rel under base and rejects any path that escapes base (e.g. a
// crafted object key containing "../"), so a hostile store cannot write outside the cache.
func resolveDest(base, rel string) (string, error) {
	dest := filepath.Join(base, rel)
	r, err := filepath.Rel(base, dest)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("s3 object key escapes cache dir: %q", rel)
	}
	return dest, nil
}

// revisionOf hashes the object set into a deterministic, order-independent
// revision: sorted "Key\tETag\n" lines over sha256.
func revisionOf(objects []objInfo) string {
	lines := make([]string, len(objects))
	for i, o := range objects {
		lines[i] = o.Key + "\t" + o.ETag + "\n"
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s S3) Sync(ctx context.Context) (string, string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return "", "", fmt.Errorf("s3 client: %w", err)
	}

	// Normalize the prefix to a directory boundary so a bare "foo" prefix does not
	// match "foobar/…" and mis-strip; the same value is used for listing and TrimPrefix.
	prefix := s.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var objects []objInfo
	for obj := range client.ListObjects(ctx, s.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return "", "", fmt.Errorf("s3 list: %w", obj.Err)
		}
		objects = append(objects, objInfo{Key: obj.Key, ETag: obj.ETag})
	}

	// Small fleet store: a full re-download is the simplest correct sync.
	if err := os.RemoveAll(s.CachePath); err != nil {
		return "", "", fmt.Errorf("s3 clear cache: %w", err)
	}
	// Ensure the checkout dir exists even when the bucket/prefix is empty; downstream
	// code expects it to be present.
	if err := os.MkdirAll(s.CachePath, 0o755); err != nil {
		return "", "", err
	}
	for _, obj := range objects {
		rel := strings.TrimPrefix(obj.Key, prefix)
		dest, err := resolveDest(s.CachePath, rel)
		if err != nil {
			return "", "", err
		}
		if err := client.FGetObject(ctx, s.Bucket, obj.Key, dest, minio.GetObjectOptions{}); err != nil {
			return "", "", fmt.Errorf("s3 get %q: %w", obj.Key, err)
		}
	}

	return s.CachePath, revisionOf(objects), nil
}
