package web

import (
	"embed"
	"net/http"
)

//go:embed index.html live.html
var files embed.FS

func pageHandler(filename string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		if filename == "live.html" {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com 'unsafe-inline'; style-src 'self' https://unpkg.com 'unsafe-inline'; img-src 'self' data: https://*.tile.openstreetmap.org; connect-src 'self'; font-src 'self'; frame-ancestors 'none'")
		}
		content, err := files.ReadFile(filename)
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
	})
}

func Handler() http.Handler {
	return pageHandler("index.html")
}

func LiveHandler() http.Handler {
	return pageHandler("live.html")
}
