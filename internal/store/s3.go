package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 syncs the store from an S3-compatible bucket into a local checkout.
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

// s3State records the last sync so the next one is incremental: which store the
// cache belongs to, and the ETag of every object then in it.
type s3State struct {
	Store   string            `json:"store"`   // endpoint|bucket|prefix identity
	Objects map[string]string `json:"objects"` // object key -> ETag
}

// statePath keeps the sync state beside the cache dir, not inside it, so it is
// never served as store content nor removed by the drop-removed pass.
func (s S3) statePath() string { return s.CachePath + ".s3state.json" }

func (s S3) storeIdentity() string { return s.Endpoint + "|" + s.Bucket + "|" + s.Prefix }

// loadS3State reads the sync state; a missing or corrupt file just yields an empty
// state, which forces a full download.
func loadS3State(path string) s3State {
	st := s3State{Objects: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	if st.Objects == nil {
		st.Objects = map[string]string{}
	}
	return st
}

func saveS3State(path string, st s3State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

	prev := loadS3State(s.statePath())
	// The cache belongs to one store; if the endpoint/bucket/prefix changed its files
	// no longer match, so start clean rather than mix two stores.
	if prev.Store != s.storeIdentity() {
		if err := os.RemoveAll(s.CachePath); err != nil {
			return "", "", fmt.Errorf("s3 clear cache: %w", err)
		}
		prev = s3State{Objects: map[string]string{}}
	}
	if err := os.MkdirAll(s.CachePath, 0o755); err != nil {
		return "", "", err
	}

	// Download only new or changed objects (by ETag); keep unchanged ones from the cache.
	next := make(map[string]string, len(objects))
	for _, obj := range objects {
		rel := strings.TrimPrefix(obj.Key, prefix)
		dest, err := resolveDest(s.CachePath, rel)
		if err != nil {
			return "", "", err
		}
		next[obj.Key] = obj.ETag
		if prev.Objects[obj.Key] == obj.ETag {
			if _, err := os.Stat(dest); err == nil {
				continue // unchanged and present on disk -> no download
			}
		}
		if err := client.FGetObject(ctx, s.Bucket, obj.Key, dest, minio.GetObjectOptions{}); err != nil {
			return "", "", fmt.Errorf("s3 get %q: %w", obj.Key, err)
		}
	}

	// Drop objects that left the store.
	for key := range prev.Objects {
		if _, ok := next[key]; ok {
			continue
		}
		if dest, err := resolveDest(s.CachePath, strings.TrimPrefix(key, prefix)); err == nil {
			os.Remove(dest) // best-effort
		}
	}

	// Persist state last, so a mid-sync failure leaves the prior state intact and the
	// next run re-derives the work rather than trusting a half-applied cache.
	if err := saveS3State(s.statePath(), s3State{Store: s.storeIdentity(), Objects: next}); err != nil {
		return "", "", fmt.Errorf("s3 save state: %w", err)
	}
	return s.CachePath, revisionOf(objects), nil
}
