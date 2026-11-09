package frontend

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// mimeTypes maps file extensions to MIME types
var mimeTypes = map[string]string{
	".js":    "application/javascript",
	".mjs":   "application/javascript",
	".css":   "text/css",
	".html":  "text/html",
	".json":  "application/json",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".eot":   "application/vnd.ms-fontobject",
	".wav":   "audio/wav",
	".mp3":   "audio/mpeg",
}

//go:embed all:dist
var distFS embed.FS

// DefaultBrandName is the product name shown when none is configured.
const DefaultBrandName = "Whatomate"

// maxLogoSize caps the size of a configured logo file.
const maxLogoSize = 200 << 10

// Brand is the product name and logo shown in the UI.
type Brand struct {
	Name string
	// Logo is an SVG served as the favicon and shown in the UI.
	// Nil keeps the embedded favicon and the default logo.
	Logo []byte
}

// LoadLogo reads an SVG logo file, rejecting files that are too large
// or do not look like SVG.
func LoadLogo(path string) ([]byte, error) {
	if !strings.EqualFold(filepath.Ext(path), ".svg") {
		return nil, errors.New("logo must be an .svg file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxLogoSize {
		return nil, fmt.Errorf("logo is larger than %d KB", maxLogoSize>>10)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxLogoSize {
		return nil, fmt.Errorf("logo is larger than %d KB", maxLogoSize>>10)
	}
	if !bytes.Contains(bytes.ToLower(data), []byte("<svg")) {
		return nil, errors.New("logo is not an SVG document")
	}
	return data, nil
}

// logoVersion returns a short content hash used to bust browser caches.
func logoVersion(logo []byte) string {
	sum := sha256.Sum256(logo)
	return hex.EncodeToString(sum[:5])
}

// renderIndex injects the base path and brand into index.html.
func renderIndex(indexContent []byte, basePath string, brand Brand) []byte {
	name := brand.Name
	if name == "" {
		name = DefaultBrandName
	}

	// Inject base tag right after <head> so it's processed before any relative URLs
	// Base tag ensures relative URLs (./assets/...) resolve from basePath, not current page path
	baseHref := basePath + "/"
	if basePath == "" {
		baseHref = "/"
	}
	baseTag := fmt.Sprintf(`<head><base href="%s">`, baseHref)
	modifiedHTML := strings.Replace(string(indexContent), "<head>", baseTag, 1)

	// Brand name in the tab title and description, HTML-escaped
	escapedName := html.EscapeString(name)
	modifiedHTML = strings.Replace(modifiedHTML, "<title>"+DefaultBrandName+"</title>", "<title>"+escapedName+"</title>", 1)
	modifiedHTML = strings.Replace(modifiedHTML, `content="`+DefaultBrandName+` - `, `content="`+escapedName+` - `, 1)

	logoURL := ""
	if brand.Logo != nil {
		version := logoVersion(brand.Logo)
		logoURL = basePath + "/favicon.svg?v=" + version
		modifiedHTML = strings.Replace(modifiedHTML, `href="./favicon.svg"`, `href="./favicon.svg?v=`+version+`"`, 1)
		modifiedHTML = strings.Replace(modifiedHTML, `href="/favicon.svg"`, `href="./favicon.svg?v=`+version+`"`, 1)
	}

	// json.Marshal escapes <, > and & so the values cannot close the script tag
	brandJSON, _ := json.Marshal(map[string]string{"name": name, "logo": logoURL})

	// Inject base path and brand script before </head>
	headScript := fmt.Sprintf(`<script>window.__BASE_PATH__ = "%s";window.__BRAND__ = %s;</script></head>`, basePath, brandJSON)
	return []byte(strings.Replace(modifiedHTML, "</head>", headScript, 1))
}

// Handler returns a fasthttp handler that serves the embedded frontend files
// basePath should be empty string for root deployment or "/subpath" for subdirectory
// If frontend is not embedded, returns a handler that shows a helpful message
func Handler(basePath string, brand Brand) fasthttp.RequestHandler {
	// Get the dist subdirectory
	distSubFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		return notEmbeddedHandler("Frontend not embedded: " + err.Error())
	}
	return newHandler(distSubFS, basePath, brand)
}

func newHandler(distSubFS fs.FS, basePath string, brand Brand) fasthttp.RequestHandler {
	// Normalize base path
	basePath = strings.TrimSuffix(basePath, "/")

	// Read and modify index.html to inject base path
	indexContent, err := fs.ReadFile(distSubFS, "index.html")
	if err != nil {
		return notEmbeddedHandler("Frontend not embedded: index.html not found. Run 'make build-prod' to embed frontend.")
	}
	indexHTML := renderIndex(indexContent, basePath, brand)

	// Create file server
	fileServer := http.FileServer(http.FS(distSubFS))

	// Wrap with SPA fallback and proper MIME types
	spaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// A configured logo replaces the embedded favicon
		if brand.Logo != nil && (path == "/favicon.svg" || path == basePath+"/favicon.svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(brand.Logo)))
			_, _ = w.Write(brand.Logo)
			return
		}

		// Try to serve the file
		if path != "/" && !strings.HasPrefix(path, "/api") {
			// Check if file exists
			filePath := strings.TrimPrefix(path, "/")
			file, err := distSubFS.Open(filePath)
			if err == nil {
				defer func() { _ = file.Close() }()

				// Get file info for size
				stat, err := file.Stat()
				if err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}

				// Skip directories
				if stat.IsDir() {
					fileServer.ServeHTTP(w, r)
					return
				}

				// Set correct Content-Type based on file extension
				ext := strings.ToLower(filepath.Ext(filePath))
				if mimeType, ok := mimeTypes[ext]; ok {
					w.Header().Set("Content-Type", mimeType)
				} else {
					w.Header().Set("Content-Type", "application/octet-stream")
				}

				// Check Accept-Encoding and serve pre-compressed if available
				acceptEncoding := r.Header.Get("Accept-Encoding")
				var content []byte
				var contentEncoding string

				// Try Brotli first (better compression)
				if strings.Contains(acceptEncoding, "br") {
					if brContent, err := fs.ReadFile(distSubFS, filePath+".br"); err == nil {
						content = brContent
						contentEncoding = "br"
					}
				}

				// Fall back to gzip
				if content == nil && strings.Contains(acceptEncoding, "gzip") {
					if gzContent, err := fs.ReadFile(distSubFS, filePath+".gz"); err == nil {
						content = gzContent
						contentEncoding = "gzip"
					}
				}

				// Fall back to uncompressed
				if content == nil {
					content, err = fs.ReadFile(distSubFS, filePath)
					if err != nil {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
						return
					}
				}

				if contentEncoding != "" {
					w.Header().Set("Content-Encoding", contentEncoding)
				}
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
				_, _ = w.Write(content)
				return
			}
		}

		// For root or non-existent files (SPA routes), serve modified index.html
		if path == "/" || (!strings.HasPrefix(path, "/api") && !strings.Contains(path, ".")) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(indexHTML)
			return
		}

		// Serve the actual file
		fileServer.ServeHTTP(w, r)
	})

	// Convert to fasthttp handler
	return fasthttpadaptor.NewFastHTTPHandler(spaHandler)
}

// IsEmbedded returns true if the frontend dist folder is embedded
func IsEmbedded() bool {
	entries, err := distFS.ReadDir("dist")
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// notEmbeddedHandler returns a handler that displays a message when frontend is not embedded
func notEmbeddedHandler(message string) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.SetContentType("text/plain; charset=utf-8")
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		_, _ = ctx.WriteString(message)
	}
}
