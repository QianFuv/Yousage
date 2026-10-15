package relay

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:web
var webFiles embed.FS

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

const placeholderHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Yousage relay</title></head><body><h1>Yousage relay</h1><p>The web viewer has not been built. Run pnpm --dir frontend build:web and rebuild the relay.</p></body></html>`

func servePage(name string, files fs.FS) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if _, isValid := requestID(writer, request); !isValid {
			return
		}
		writer.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := fs.ReadFile(files, "web/dist/"+name)
		if err != nil {
			data = []byte(placeholderHTML)
		}
		writer.Write(data)
	}
}

func serveAsset(files fs.FS) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, "/")
		if !fs.ValidPath(name) {
			http.NotFound(writer, request)
			return
		}
		data, err := fs.ReadFile(files, "web/dist/"+name)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			writer.Header().Set("Content-Type", contentType)
		}
		http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(data))
	}
}
