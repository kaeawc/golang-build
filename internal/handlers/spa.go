package handlers

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type SPAHandler struct {
	staticPath string
	indexPath  string
	files      http.Handler
}

func NewSPAHandler(staticPath, indexPath string) *SPAHandler {
	return &SPAHandler{
		staticPath: staticPath,
		indexPath:  indexPath,
		files:      http.FileServer(http.Dir(staticPath)),
	}
}

func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleaned := path.Clean("/" + r.URL.Path)
	fsPath := filepath.Join(h.staticPath, filepath.FromSlash(cleaned))

	info, err := os.Stat(fsPath)
	if os.IsNotExist(err) || (err == nil && info.IsDir()) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(h.staticPath, h.indexPath))
		return
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if strings.HasPrefix(cleaned, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	h.files.ServeHTTP(w, r)
}
