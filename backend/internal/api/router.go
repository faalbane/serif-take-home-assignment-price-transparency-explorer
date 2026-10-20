package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// Options configures the HTTP surface.
type Options struct {
	// StaticDir serves the built frontend. Empty disables static serving,
	// which is the normal case in development where Vite serves the SPA.
	StaticDir string
	// AllowedOrigins are the CORS origins permitted for the API. The Vite dev
	// server runs on a different port, so development needs this; production
	// serves the SPA from the same origin and needs none.
	AllowedOrigins []string
	Logger         *log.Logger
}

// Routes returns the full HTTP handler.
func (s *Server) Routes(opt Options) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.Health)
	mux.HandleFunc("GET /api/meta", s.Meta)
	mux.HandleFunc("GET /api/codes", s.Codes)
	mux.HandleFunc("GET /api/providers", s.Providers)
	mux.HandleFunc("GET /api/rates", s.Rates)
	mux.HandleFunc("GET /api/rates.csv", s.RatesCSV)

	// Any other /api path is a 404 in JSON, not an HTML page.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown endpoint", r.URL.Path,
			"Available: /api/health, /api/meta, /api/codes, /api/providers, /api/rates, /api/rates.csv")
	})

	if opt.StaticDir != "" {
		mux.Handle("/", spaHandler(opt.StaticDir))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"service": "price-transparency-explorer",
				"note":    "The API is running. The UI is served by the Vite dev server in development.",
				"endpoints": []string{
					"/api/health", "/api/meta", "/api/codes", "/api/providers",
					"/api/rates?code=10006", "/api/rates.csv?code=10006",
				},
			})
		})
	}

	var h http.Handler = mux
	h = withCORS(h, opt.AllowedOrigins)
	h = withRecover(h, opt.Logger)
	h = withLogging(h, opt.Logger)
	return h
}

// spaHandler serves the built frontend, falling back to index.html so client
// routing and deep links work.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		// filepath.Clean already removes traversal, but reject anything that
		// still tries to climb out rather than trusting a single guard.
		if strings.HasPrefix(clean, "..") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(dir, clean)
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			// Hashed asset filenames are safe to cache aggressively; the entry
			// HTML must not be.
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fs.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func withCORS(next http.Handler, allowed []string) http.Handler {
	allow := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		allow[strings.TrimSpace(o)] = true
	}
	wildcard := allow["*"]

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (wildcard || allow[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withRecover converts a panic into a 500 so one bad request cannot take the
// process down. The error-handling requirement is explicit about the app not
// crashing, and this is the backstop for everything the handlers did not
// anticipate.
func withRecover(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if logger != nil {
					logger.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				}
				writeError(w, http.StatusInternalServerError, "internal error",
					"The server hit an unexpected condition handling this request.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func withLogging(next http.Handler, logger *log.Logger) http.Handler {
	if logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		// Health polling during a cold start would otherwise drown the log.
		if r.URL.Path == "/api/health" && rec.status == http.StatusOK {
			return
		}
		logger.Printf("%s %s%s %d %dB %s",
			r.Method, r.URL.Path, querySuffix(r.URL.RawQuery), rec.status, rec.bytes,
			time.Since(start).Round(time.Microsecond))
	})
}

func querySuffix(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > 200 {
		raw = raw[:200] + "..."
	}
	return "?" + raw
}
