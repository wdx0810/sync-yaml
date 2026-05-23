package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/store"
)

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.sourceStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Mask sensitive fields.
	for i := range sources {
		if sources[i].Token != "" {
			sources[i].Token = "***"
		}
		if sources[i].WebhookSecret != "" {
			sources[i].WebhookSecret = "***"
		}
	}
	writeJSON(w, http.StatusOK, sources)
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var src store.GitLabSource
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if src.Name == "" || src.URL == "" || src.Token == "" {
		writeError(w, http.StatusBadRequest, "name, url, and token are required")
		return
	}
	if src.Branch == "" {
		src.Branch = "main"
	}
	if src.Path == "" {
		src.Path = "/"
	}

	if err := s.sourceStore.Create(&src); err != nil {
		if _, ok := err.(*store.ErrDuplicate); ok {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	var src store.GitLabSource
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.sourceStore.Update(name, &src); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if err := s.sourceStore.Delete(name); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if _, ok := err.(*store.ErrReferenced); ok {
			writeError(w, 422, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	src, err := s.sourceStore.Get(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.connTester.TestGitLab(src.URL, src.Token, src.ProjectID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}
