package main

import (
	"context"
	"database/sql"
	"fmt"
	log "log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func connectDB() (*sql.DB, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("missing DATABASE_URL")
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return db, nil
}

func generateShortLink() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length := 6
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

type app struct {
	db *sql.DB
}

type createShortLinkCommand struct {
	LongURL   string
	IP        string
	UserAgent string
}

func (a *app) CreateShortLink(ctx context.Context, cmd createShortLinkCommand) (string, error) {
	shortLink := generateShortLink()
	_, err := a.db.Exec("INSERT INTO short_links (short_link, long_url, create_time, ip, user_agent) VALUES ($1, $2, now(), $3, $4)",
		shortLink, cmd.LongURL, cmd.IP, cmd.UserAgent)
	if err != nil {
		return "", err
	}
	return shortLink, nil
}

type getLongURLQuery struct {
	ShortLink string
}

func (a *app) LongURL(ctx context.Context, query getLongURLQuery) (string, error) {
	var longURL string
	err := a.db.QueryRow("SELECT long_url FROM short_links WHERE short_link = $1", query.ShortLink).Scan(&longURL)
	if err != nil {
		return "", err
	}
	return longURL, nil
}

type httpHandler struct {
	http.ServeMux
	app *app
}

func (h *httpHandler) createShortLink(w http.ResponseWriter, r *http.Request) {
	longURL := r.FormValue("long_url")
	if longURL == "" {
		http.Error(w, "Missing long URL", http.StatusBadRequest)
		return
	}

	// check for X-Forwarded-For header
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		r.RemoteAddr = ip
	}
	ip := r.RemoteAddr

	userAgent := r.UserAgent()
	shortLink, err := h.app.CreateShortLink(r.Context(), createShortLinkCommand{
		LongURL:   longURL,
		IP:        ip,
		UserAgent: userAgent,
	})
	if err != nil {
		log.Error("Failed to create short link", "error", err)
		http.Error(w, "Failed to create short link", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("http://%s/%s", r.Host, shortLink)
	Response(url).Render(r.Context(), w)
}

func (h *httpHandler) redirect(w http.ResponseWriter, r *http.Request) {
	shortLink := r.URL.Path[1:]
	if shortLink == "" {
		http.Error(w, "Invalid short link", http.StatusBadRequest)
		return
	}

	longURL, err := h.app.LongURL(r.Context(), getLongURLQuery{ShortLink: shortLink})
	if err != nil {
		http.Error(w, "Short link not found", http.StatusNotFound)
		return
	}

	http.Redirect(w, r, longURL, http.StatusFound)
}

func (h *httpHandler) init() {
	h.HandleFunc("/create", h.createShortLink)
	h.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			Index().Render(r.Context(), w)
		} else {
			h.redirect(w, r)
		}
	})
}

func NewHTTPHandler(app *app) http.Handler {
	h := &httpHandler{app: app}
	h.init()
	return panicHandler(h)
}

func main() {
	db, err := connectDB()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	a := &app{db: db}
	h := NewHTTPHandler(a)
	port := os.Getenv("PORT")
	port = strings.TrimSpace(port)
	if port == "" {
		port = "8080"
	}
	http.ListenAndServe(fmt.Sprintf(":%s", port), h)
}

func panicHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic while handling http request", "recover", r)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	})
}
