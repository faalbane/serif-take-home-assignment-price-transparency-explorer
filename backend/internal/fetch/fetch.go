// Package fetch retrieves remote files with a local disk cache.
//
// It knows nothing about the CMS file format. Its only job is to turn a URL
// into an io.ReadCloser as cheaply and as reliably as possible, because the
// in-network rate files are hundreds of megabytes and a reviewer should pay
// that download cost once rather than on every restart.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fetcher retrieves URLs, optionally caching responses on disk.
type Fetcher struct {
	Client   *http.Client
	CacheDir string
	// MaxAttempts is the total number of tries per URL, including the first.
	MaxAttempts int
	// Logf receives progress messages. Nil disables logging.
	Logf func(format string, args ...any)
	// AllowLocalFiles permits file:// URLs and bare paths, which the tests and
	// the offline demo mode rely on.
	AllowLocalFiles bool
}

// New returns a Fetcher with timeouts suited to multi-hundred-megabyte files.
func New(cacheDir string) *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			// No overall client timeout: a 174 MB body on a slow link can
			// legitimately take minutes. Per-attempt deadlines come from the
			// context passed to Open.
			Transport: &http.Transport{
				MaxIdleConns:          8,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		CacheDir:    cacheDir,
		MaxAttempts: 3,
	}
}

// Result describes where a body came from.
type Result struct {
	Body io.ReadCloser
	// Bytes is the expected size when the server reported one, else -1.
	Bytes int64
	// FromCache reports whether the body was served from the local cache.
	FromCache bool
	// CachePath is the on-disk location, when caching is enabled.
	CachePath string
}

// ErrNotCached is returned by OpenCachedOnly when the URL is absent from the
// cache.
var ErrNotCached = errors.New("not present in local cache")

func (f *Fetcher) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(format, args...)
	}
}

// Open returns a reader for the URL, using the disk cache when possible.
//
// On a cache miss the response is streamed to a temporary file and then renamed
// into place, so an interrupted download can never be mistaken for a complete
// one. The caller reads from the cached file rather than from the network,
// which keeps ingest timing identical between cold and warm starts.
func (f *Fetcher) Open(ctx context.Context, rawURL string) (*Result, error) {
	if isLocal(rawURL) {
		if !f.AllowLocalFiles {
			return nil, fmt.Errorf("local file URLs are not permitted: %s", rawURL)
		}
		return f.openLocal(rawURL)
	}

	if f.CacheDir != "" {
		path := f.cachePath(rawURL)
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			file, err := os.Open(path)
			if err == nil {
				f.logf("cache hit: %s (%s)", rawURL, humanBytes(st.Size()))
				return &Result{Body: file, Bytes: st.Size(), FromCache: true, CachePath: path}, nil
			}
		}
	}
	return f.download(ctx, rawURL)
}

// OpenCachedOnly returns a cached body without touching the network. Used by
// offline mode so a reviewer with no connectivity still gets a working app.
func (f *Fetcher) OpenCachedOnly(rawURL string) (*Result, error) {
	if isLocal(rawURL) {
		return f.openLocal(rawURL)
	}
	if f.CacheDir == "" {
		return nil, ErrNotCached
	}
	path := f.cachePath(rawURL)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotCached, rawURL)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Result{Body: file, Bytes: st.Size(), FromCache: true, CachePath: path}, nil
}

func (f *Fetcher) openLocal(rawURL string) (*Result, error) {
	path := strings.TrimPrefix(rawURL, "file://")
	// Fixture URLs carry a "#n" discriminator so one file can stand in for
	// several index entries; it is not part of the path.
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open local file %s: %w", path, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local file %s: %w", path, err)
	}
	return &Result{Body: file, Bytes: st.Size(), CachePath: path}, nil
}

func (f *Fetcher) download(ctx context.Context, rawURL string) (*Result, error) {
	attempts := f.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(1<<uint(attempt-2)) * time.Second
			f.logf("retrying %s in %s (attempt %d/%d) after: %v", rawURL, backoff, attempt, attempts, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		res, err := f.downloadOnce(ctx, rawURL)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() != nil || !retryable(err) {
			break
		}
	}
	return nil, fmt.Errorf("fetch %s: %w", rawURL, lastErr)
}

func (f *Fetcher) downloadOnce(ctx context.Context, rawURL string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// Identify the client. Payer CDNs sometimes reject empty user agents.
	req.Header.Set("User-Agent", "price-transparency-explorer/1.0 (+https://github.com/faalbane/price-transparency-explorer)")
	req.Header.Set("Accept", "application/json, */*")
	// Accept-Encoding is deliberately left to net/http. Setting it manually
	// disables transparent decompression, which would cache compressed bytes
	// and hand them to the JSON decoder as-is.

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &httpError{Status: resp.StatusCode, URL: rawURL}
	}

	body := resp.Body

	if f.CacheDir == "" {
		return &Result{Body: body, Bytes: resp.ContentLength}, nil
	}

	path := f.cachePath(rawURL)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		body.Close()
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partial-*")
	if err != nil {
		body.Close()
		return nil, fmt.Errorf("create cache temp file: %w", err)
	}
	tmpName := tmp.Name()

	f.logf("downloading %s (%s)", rawURL, humanBytes(resp.ContentLength))
	written, copyErr := io.Copy(tmp, body)
	body.Close()
	closeErr := tmp.Close()

	if copyErr != nil || closeErr != nil {
		os.Remove(tmpName)
		if copyErr != nil {
			return nil, fmt.Errorf("stream body to cache: %w", copyErr)
		}
		return nil, fmt.Errorf("close cache temp file: %w", closeErr)
	}
	// A short body means a truncated transfer; treat it as a failure so the
	// retry loop gets a chance rather than caching a broken file. ContentLength
	// is -1 when the response was transparently decompressed, in which case
	// there is nothing to compare against.
	if resp.ContentLength > 0 && written != resp.ContentLength {
		os.Remove(tmpName)
		return nil, &truncatedError{Want: resp.ContentLength, Got: written}
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("commit cache file: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reopen cache file: %w", err)
	}
	f.logf("cached %s -> %s (%s)", rawURL, path, humanBytes(written))
	return &Result{Body: file, Bytes: written, CachePath: path}, nil
}

// cachePath derives a stable, collision-free filename for a URL. The hash
// prefix guarantees uniqueness; the readable suffix makes the cache directory
// inspectable by a human.
func (f *Fetcher) cachePath(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	hash := hex.EncodeToString(sum[:])[:16]

	name := "file.json"
	if u, err := url.Parse(rawURL); err == nil {
		if base := filepath.Base(u.Path); base != "" && base != "." && base != "/" {
			if dec, err := url.PathUnescape(base); err == nil {
				base = dec
			}
			name = sanitize(base)
		}
	}
	return filepath.Join(f.CacheDir, hash+"-"+name)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[len(out)-120:]
	}
	return out
}

func isLocal(rawURL string) bool {
	if strings.HasPrefix(rawURL, "file://") {
		return true
	}
	return !strings.Contains(rawURL, "://")
}

type httpError struct {
	Status int
	URL    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d", e.Status)
}

type truncatedError struct {
	Want, Got int64
}

func (e *truncatedError) Error() string {
	return fmt.Sprintf("truncated download: got %d of %d bytes", e.Got, e.Want)
}

// retryable reports whether another attempt could plausibly succeed. A 404 will
// never fix itself; a 503 or a dropped connection might.
func retryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	var te *truncatedError
	if errors.As(err, &te) {
		return true
	}
	return true // network-level errors
}

func humanBytes(n int64) string {
	if n < 0 {
		return "unknown size"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(n)
	for _, u := range units {
		val /= unit
		if val < unit {
			return fmt.Sprintf("%.1f %s", val, u)
		}
	}
	return fmt.Sprintf("%.1f PB", val/unit)
}
