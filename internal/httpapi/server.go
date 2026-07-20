// Package httpapi 提供 memory-mcp 的 REST JSON API：Server 把 db.Store
// 包成 HTTP handler（給中央機器常駐），Client 則是反向實作 db.Store，
// 把每個操作轉發成 HTTP 呼叫（給遠端機器用 --remote 連過去）。
// 設計給 SSH tunnel／VPN 這種已授權的私有連線用，不做額外認證。
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"memory-mcp/internal/db"
)

// Server 把 db.Store 包成 HTTP handler。
type Server struct {
	store db.Store
}

// NewServer 建立 REST API server。
func NewServer(s db.Store) *Server {
	return &Server{store: s}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// Handler 建立掛好所有路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/store", func(w http.ResponseWriter, r *http.Request) {
		var mem db.Memory
		if err := json.NewDecoder(r.Body).Decode(&mem); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		id, err := s.store.Store(&mem)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"id": id})
	})

	mux.HandleFunc("GET /v1/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		m, err := s.store.Get(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})

	mux.HandleFunc("PUT /v1/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.store.Update(id, body.Content); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
	})

	mux.HandleFunc("DELETE /v1/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.store.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	})

	mux.HandleFunc("GET /v1/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		results, err := s.store.Search(db.SearchOptions{
			Query: q.Get("q"), Type: q.Get("type"), Limit: limit,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
	})

	mux.HandleFunc("GET /v1/list", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		memories, err := s.store.List(db.ListOptions{
			Type: q.Get("type"), Limit: limit, Since: q.Get("since"),
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	})

	mux.HandleFunc("GET /v1/stats", func(w http.ResponseWriter, r *http.Request) {
		s, err := s.store.Stats()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})

	mux.HandleFunc("GET /v1/context", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		summary, err := s.store.Context(db.ContextOptions{
			Type: q.Get("type"), Project: q.Get("project"), Limit: limit,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, summary)
	})

	mux.HandleFunc("GET /v1/export", func(w http.ResponseWriter, r *http.Request) {
		memories, err := s.store.ExportAll()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	})

	mux.HandleFunc("POST /v1/import", func(w http.ResponseWriter, r *http.Request) {
		var memories []db.Memory
		if err := json.NewDecoder(r.Body).Decode(&memories); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		n, err := s.store.ImportBatch(memories)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"imported": n})
	})

	return mux
}
