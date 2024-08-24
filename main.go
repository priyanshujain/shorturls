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

	"bytes"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/skip2/go-qrcode"
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
	db            *sql.DB
	dataDirectory string
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

type createQRCodeCommand struct {
	LongURL   string
	IP        string
	UserAgent string
}

func (a *app) CreateQRCode(ctx context.Context, cmd createQRCodeCommand) (string, error) {
	qrId := uuid.New().String()
	qr, err := qrcode.New(cmd.LongURL, qrcode.Medium)
	if err != nil {
		return "", err
	}

	// save the QR code image to a file
	err = qr.WriteFile(256, fmt.Sprintf("%s/%s.png", a.dataDirectory, qrId))
	if err != nil {
		return "", err
	}

	_, err = a.db.Exec("INSERT INTO qr_codes (qr_id, long_url, create_time, ip, user_agent) VALUES ($1, $2, now(), $3, $4)",
		qrId, cmd.LongURL, cmd.IP, cmd.UserAgent)
	if err != nil {
		return "", err
	}
	return qrId, nil
}

type qrCodeQuery struct {
	QRID string
}

type qrCode struct {
	ID         string
	LongURL    string
	CreateTime time.Time
	Content    []byte
}

func (a *app) QRCode(ctx context.Context, query qrCodeQuery) (qrCode, error) {
	var qr qrCode
	err := a.db.QueryRow("SELECT qr_id, long_url, create_time FROM qr_codes WHERE qr_id = $1", query.QRID).Scan(&qr.ID, &qr.LongURL, &qr.CreateTime)
	if err != nil {
		return qrCode{}, fmt.Errorf("QR code not found: %w", err)
	}

	content, err := os.ReadFile(fmt.Sprintf("%s/%s.png", a.dataDirectory, query.QRID))
	if err != nil {
		return qrCode{}, fmt.Errorf("failed to read QR code image: %w", err)
	}

	qr.Content = content
	return qr, nil
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
	LinkResponse(url).Render(r.Context(), w)
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

func (h *httpHandler) createQRCode(w http.ResponseWriter, r *http.Request) {
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
	shortLink, err := h.app.CreateQRCode(r.Context(), createQRCodeCommand{
		LongURL:   longURL,
		IP:        ip,
		UserAgent: userAgent,
	})
	if err != nil {
		log.Error("Failed to create qr code", "error", err)
		http.Error(w, "Failed to create qr code", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("http://%s/qrcodes/%s.png", r.Host, shortLink)
	QRRResponse(url, longURL).Render(r.Context(), w)
}

func (h *httpHandler) qrCode(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Path[len("/qrcodes/"):]
	qrID := strings.TrimSuffix(fileName, ".png")
	qr, err := h.app.QRCode(r.Context(), qrCodeQuery{QRID: qrID})
	if err != nil {
		log.Error("unsable to get wt image", "error", err)
		http.Error(w, "QR image not found", http.StatusNotFound)
		return
	}

	objectName := fmt.Sprintf("%s.png", qrID)

	w.Header().Set("Content-Type", "image/png")
	http.ServeContent(w, r, objectName, qr.CreateTime, bytes.NewReader(qr.Content))
}

func (h *httpHandler) init() {
	h.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
		source := r.FormValue("source")
		if source == "qr" {
			h.createQRCode(w, r)
		} else {
			h.createShortLink(w, r)
		}
	})
	h.HandleFunc("/qrcodes/", h.qrCode)
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

	dataDir := os.Getenv("DATA_DIR")
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = "data"
	}

	a := &app{db: db, dataDirectory: dataDir}
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
