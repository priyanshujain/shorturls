package main

import (
	"database/sql"
	"fmt"
	log "log/slog"
	"math/rand"
	"net/http"
	"os"
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

func createShortLink(db *sql.DB, longURL, ip string) (string, error) {
	shortLink := generateShortLink()
	_, err := db.Exec("INSERT INTO short_links (short_link, long_url, create_time, ip) VALUES ($1, $2, now(), $3)",
		shortLink, longURL, ip)
	if err != nil {
		return "", err
	}
	return shortLink, nil
}

func getLongURL(db *sql.DB, shortLink string) (string, error) {
	var longURL string
	err := db.QueryRow("SELECT long_url FROM short_links WHERE short_link = $1", shortLink).Scan(&longURL)
	if err != nil {
		return "", err
	}
	return longURL, nil
}

func createLinkHandler(w http.ResponseWriter, r *http.Request) {
	db, err := connectDB()
	if err != nil {
		log.Error("Failed to connect to database", "error", err)
		http.Error(w, "Failed to connect to database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	longURL := r.FormValue("long_url")
	if longURL == "" {
		http.Error(w, "Missing long URL", http.StatusBadRequest)
		return
	}

	ip := r.RemoteAddr
	shortLink, err := createShortLink(db, longURL, ip)
	if err != nil {
		log.Error("Failed to create short link", "error", err)
		http.Error(w, "Failed to create short link", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("http://%s/%s", r.Host, shortLink)
	Response(url).Render(r.Context(), w)
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	db, err := connectDB()
	if err != nil {
		http.Error(w, "Failed to connect to database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	shortLink := r.URL.Path[1:]
	if shortLink == "" {
		http.Error(w, "Invalid short link", http.StatusBadRequest)
		return
	}

	longURL, err := getLongURL(db, shortLink)
	if err != nil {
		http.Error(w, "Short link not found", http.StatusNotFound)
		return
	}

	http.Redirect(w, r, longURL, http.StatusFound)
}

func main() {
	db, err := connectDB()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	http.HandleFunc("/create", createLinkHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			Index().Render(r.Context(), w)
		} else {
			redirectHandler(w, r)
		}
	})

	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
