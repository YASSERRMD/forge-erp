package documentsvc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// S3Config carries S3-compatible object storage settings (env FERP_S3_*).
// Defaults target the compose MinIO service (path-style, no TLS).
type S3Config struct {
	Endpoint  string // host:port, e.g. localhost:9000
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Timeout   time.Duration
}

// LoadS3Config reads FERP_S3_* env with MinIO-compose defaults.
func LoadS3Config(get func(key, def string) string) S3Config {
	return S3Config{
		Endpoint:  get("FERP_S3_ENDPOINT", "localhost:9000"),
		Bucket:    get("FERP_S3_BUCKET", "forgeerp"),
		Region:    get("FERP_S3_REGION", "us-east-1"),
		AccessKey: get("FERP_S3_ACCESS_KEY", "forgeerp"),
		SecretKey: get("FERP_S3_SECRET_KEY", "forgeerp123"),
		UseSSL:    get("FERP_S3_SSL", "0") == "1",
		Timeout:   30 * time.Second,
	}
}

// S3Storage implements Storage against any S3-compatible service (MinIO)
// with hand-rolled SigV4 (stdlib only). Keys map to /{bucket}/{key}
// path-style so MinIO works without virtual-host DNS.
type S3Storage struct {
	cfg    S3Config
	client *http.Client
	now    func() time.Time
}

// NewS3Storage builds an S3 backend (bucket must exist; created out-of-band
// or by the MinIO console — see RUNBOOK).
func NewS3Storage(cfg S3Config) *S3Storage {
	return &S3Storage{cfg: cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		now:    time.Now}
}

// scheme returns http/https per config.
func (s *S3Storage) scheme() string {
	if s.cfg.UseSSL {
		return "https"
	}
	return "http"
}

// objectURL builds the path-style object URL (key segments escaped).
func (s *S3Storage) objectURL(key string) string {
	segs := strings.Split(strings.Trim(key, "/"), "/")
	for i, p := range segs {
		segs[i] = url.PathEscape(p)
	}
	return fmt.Sprintf("%s://%s/%s/%s", s.scheme(), s.cfg.Endpoint, s.cfg.Bucket, strings.Join(segs, "/"))
}

// sign applies SigV4 to req with the given payload hash.
func (s *S3Storage) sign(req *http.Request, payloadHash string) {
	t := s.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		"host:" + req.URL.Host + "\n" +
			"x-amz-content-sha256:" + payloadHash + "\n" +
			"x-amz-date:" + amzDate + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.cfg.Region)
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256hex(canonical)
	key := hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), dateStamp)
	key = hmacSHA256(key, s.cfg.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.cfg.AccessKey, scope, signedHeaders, sig))
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// Put uploads bytes (buffered for hashing; document sizes fit this profile).
func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader, _ int64, mime string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("documentsvc: read body: %w", err)
	}
	payloadHash := sha256hex(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	if mime == "" {
		mime = "application/octet-stream"
	}
	req.Header.Set("Content-Type", mime)
	s.sign(req, payloadHash)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("documentsvc: s3 put: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("documentsvc: s3 put status %d", resp.StatusCode)
	}
	return nil
}

// Get downloads bytes (404 → not found, matching DirStorage semantics).
func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, sha256hex(""))
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("documentsvc: s3 get: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("documentsvc: %s not found", key)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("documentsvc: s3 get status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Delete removes bytes (missing keys are success, matching DirStorage).
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	s.sign(req, sha256hex(""))
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("documentsvc: s3 delete: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("documentsvc: s3 delete status %d", resp.StatusCode)
	}
	return nil
}

// S3Enabled reports whether FERP_STORAGE_BACKEND=s3 selects MinIO/S3;
// anything else keeps the local directory backend (wired by the caller).
func S3Enabled(get func(key, def string) string) bool {
	if v := os.Getenv("FERP_STORAGE_BACKEND"); v != "" {
		return v == "s3"
	}
	return get("FERP_STORAGE_BACKEND", "dir") == "s3"
}
