package web

import (
	"embed"
	"net/http"
)

//go:embed auth.js clients.html home.html i18n.js index.html live.html
var files embed.FS

func pageHandler(filename string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		if filename == "live.html" {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com 'unsafe-inline'; style-src 'self' https://unpkg.com 'unsafe-inline'; img-src 'self' data: https://*.tile.openstreetmap.org; connect-src 'self'; font-src 'self'; frame-ancestors 'none'")
		}
		if filename == "home.html" || filename == "index.html" || filename == "clients.html" {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
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

func HomeHandler() http.Handler {
	return pageHandler("home.html")
}

func LiveHandler() http.Handler {
	return pageHandler("live.html")
}

func ClientsHandler() http.Handler {
	return pageHandler("clients.html")
}

func scriptHandler(filename string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		content, err := files.ReadFile(filename)
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(content)
	})
}

func I18nScriptHandler() http.Handler {
	return scriptHandler("i18n.js")
}

func AuthScriptHandler() http.Handler {
	return scriptHandler("auth.js")
}
