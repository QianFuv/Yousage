package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxBodyBytes = 4 << 20

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// Config supplies the owner token, storage directory, and optional test clock.
type Config struct {
	// Token authorizes writes and must contain at least 24 characters.
	Token string
	// DataDir contains relay.db and defaults to ./data.
	DataDir string
	// Now supplies the current time and defaults to time.Now.
	Now func() time.Time
}

// Server owns the relay HTTP handler, SQLite database, and expiry sweeper.
type Server struct {
	store      *blobStore
	tokenHash  [sha256.Size]byte
	now        func() time.Time
	handler    http.Handler
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	closeError error
}

// New opens relay.db and starts an hourly expiry sweep. Close releases its resources.
func New(config Config) (*Server, error) {
	if utf8.RuneCountInString(config.Token) < 24 {
		return nil, errors.New("YOUSAGE_RELAY_TOKEN must contain at least 24 characters")
	}
	if config.DataDir == "" {
		config.DataDir = "./data"
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	store, err := openStore(config.DataDir)
	if err != nil {
		return nil, err
	}
	server := &Server{
		store: store, tokenHash: sha256.Sum256([]byte(config.Token)), now: config.Now,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	server.handler = server.routes()
	go server.sweepLoop()
	return server, nil
}

// ServeHTTP serves the relay API and embedded web pages.
func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") {
		writer.Header().Set("Cache-Control", "no-store")
	}
	server.handler.ServeHTTP(writer, request)
}

// Close stops the expiry sweeper and closes SQLite after HTTP requests have drained.
func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		close(server.stop)
		<-server.done
		server.closeError = server.store.close()
	})
	return server.closeError
}

// Serve accepts HTTP requests until cancellation, then drains them for up to ten seconds.
// The caller must Close the relay after Serve returns.
func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{
		Handler: server, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute,
	}
	result := make(chan error, 1)
	go func() { result <- httpServer.Serve(listener) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := httpServer.Shutdown(shutdownContext)
		if err != nil {
			httpServer.Close()
		}
		<-result
		return err
	}
}

// Sweep deletes expired reservations and filled shares using the configured clock.
func (server *Server) Sweep(ctx context.Context) error {
	return server.store.sweep(ctx, server.now())
}

func (server *Server) sweepLoop() {
	defer close(server.done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-server.stop:
			return
		case <-ticker.C:
			server.Sweep(context.Background())
		}
	}
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/auth", server.authorize(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("PUT /api/spaces/{id}", server.authorize(server.writeSpace))
	mux.HandleFunc("GET /api/spaces/{id}", server.readBlob("space"))
	mux.HandleFunc("DELETE /api/spaces/{id}", server.authorize(server.deleteBlob("space")))
	mux.HandleFunc("POST /api/shares/reserve", server.authorize(server.reserveShare))
	mux.HandleFunc("PUT /api/shares/{id}", server.authorize(server.fillShare))
	mux.HandleFunc("GET /api/shares/{id}", server.readBlob("share"))
	mux.HandleFunc("DELETE /api/shares/{id}", server.authorize(server.deleteBlob("share")))
	mux.HandleFunc("GET /v/{id}", servePage("viewer.html", webFiles))
	mux.HandleFunc("GET /s/{id}", servePage("share.html", webFiles))
	mux.HandleFunc("GET /assets/", serveAsset(webFiles))
	return mux
}

func (server *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		token, hasBearer := strings.CutPrefix(header, "Bearer ")
		digest := sha256.Sum256([]byte(token))
		isMatch := subtle.ConstantTimeCompare(digest[:], server.tokenHash[:]) == 1
		if !hasBearer || !isMatch {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(writer, request)
	}
}

func requestID(writer http.ResponseWriter, request *http.Request) (string, bool) {
	id := request.PathValue("id")
	if !validID.MatchString(id) {
		http.Error(writer, "invalid id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func readEnvelope(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	data, err := io.ReadAll(request.Body)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			http.Error(writer, "body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(writer, "invalid body", http.StatusBadRequest)
		}
		return nil, false
	}
	if len(data) == 0 || data[0] != 0x01 {
		http.Error(writer, "unsupported envelope version", http.StatusBadRequest)
		return nil, false
	}
	return data, true
}

func (server *Server) writeSpace(writer http.ResponseWriter, request *http.Request) {
	id, isValid := requestID(writer, request)
	if !isValid {
		return
	}
	data, isValid := readEnvelope(writer, request)
	if !isValid {
		return
	}
	if err := server.store.replaceSpace(request.Context(), id, data, server.now()); err != nil {
		http.Error(writer, "storage unavailable", http.StatusInternalServerError)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// readBlob serves one consistent version and avoids loading unchanged space bodies.
func (server *Server) readBlob(kind string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		id, isValid := requestID(writer, request)
		if !isValid {
			return
		}
		now := server.now()
		condition := request.Header.Get("If-None-Match")
		var value blob
		var err error
		isUnchanged := false
		if kind == "space" && condition != "" {
			value, err = server.store.readSpaceValidator(request.Context(), id, now)
			isUnchanged = err == nil && matchesEtag(condition, value.etag)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				http.Error(writer, "storage unavailable", http.StatusInternalServerError)
				return
			}
		}
		if !isUnchanged {
			value, err = server.store.read(request.Context(), kind, id, now)
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(writer, request)
			return
		}
		if err != nil {
			http.Error(writer, "storage unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		if kind == "space" {
			writer.Header().Set("ETag", value.etag)
			writer.Header().Set("Last-Modified", value.updatedAt.Format(http.TimeFormat))
			if isUnchanged || matchesEtag(condition, value.etag) {
				writer.WriteHeader(http.StatusNotModified)
				return
			}
		}
		writer.Write(value.data)
	}
}

// matchesEtag preserves the relay's existing weak and comma-separated validators.
func matchesEtag(condition, etag string) bool {
	for _, candidate := range strings.Split(condition, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

func (server *Server) reserveShare(writer http.ResponseWriter, request *http.Request) {
	ttlDays, err := strconv.Atoi(request.URL.Query().Get("ttlDays"))
	if err != nil || (ttlDays != 1 && ttlDays != 7 && ttlDays != 14 && ttlDays != 30) {
		http.Error(writer, "ttlDays must be 1, 7, 14, or 30", http.StatusBadRequest)
		return
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		http.Error(writer, "id generation unavailable", http.StatusInternalServerError)
		return
	}
	id := base64.RawURLEncoding.EncodeToString(randomID[:])
	now := server.now().UTC()
	expiresAt := now.Add(time.Duration(ttlDays) * 24 * time.Hour)
	if err := server.store.reserveShare(request.Context(), id, now, expiresAt); err != nil {
		http.Error(writer, "storage unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"id": id, "expiresAt": expiresAt.Format(time.RFC3339Nano)})
}

func (server *Server) fillShare(writer http.ResponseWriter, request *http.Request) {
	id, isValid := requestID(writer, request)
	if !isValid {
		return
	}
	data, isValid := readEnvelope(writer, request)
	if !isValid {
		return
	}
	isFilled, err := server.store.fillShare(request.Context(), id, data, server.now())
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		http.Error(writer, "storage unavailable", http.StatusInternalServerError)
		return
	}
	if !isFilled {
		http.Error(writer, "share already filled", http.StatusConflict)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) deleteBlob(kind string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		id, isValid := requestID(writer, request)
		if !isValid {
			return
		}
		if err := server.store.delete(request.Context(), kind, id); err != nil {
			http.Error(writer, "storage unavailable", http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(value)
}
