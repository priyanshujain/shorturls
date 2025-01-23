package main

import (
	"bytes"
	"context"
	"fmt"
	log "log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/skip2/go-qrcode"
)

type app struct {
	dataDirectory string
}

type createQRCodeCommand struct {
	LongURL string
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
	return qrId, nil
}

func (a *app) QRCode(ctx context.Context, q qrCodeQuery) (qrCode, error) {
	var qr qrCode
	content, err := os.ReadFile(fmt.Sprintf("%s/%s.png", a.dataDirectory, q.QRID))
	if err != nil {
		return qrCode{}, fmt.Errorf("failed to read QR code image: %w", err)
	}
	qr.Content = content
	qr.ID = q.QRID
	return qr, nil
}

type qrCodeQuery struct {
	QRID string
}

type qrCode struct {
	ID      string
	Content []byte
}

type httpHandler struct {
	http.ServeMux
	app *app
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
	shortLink, err := h.app.CreateQRCode(r.Context(), createQRCodeCommand{
		LongURL: longURL,
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
	http.ServeContent(w, r, objectName, time.Now(), bytes.NewReader(qr.Content))
}

func (h *httpHandler) init() {
	h.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
		h.createQRCode(w, r)
	})
	h.HandleFunc("/qrcodes/", h.qrCode)
	h.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			Index().Render(r.Context(), w)
		} else {
			http.NotFound(w, r)
		}
	})
}

func NewHTTPHandler(app *app) http.Handler {
	h := &httpHandler{app: app}
	h.init()
	return panicHandler(h)
}

func main() {
	dataDir := os.Getenv("DATA_DIR")
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = "data"
	}

	a := &app{dataDirectory: dataDir}
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
