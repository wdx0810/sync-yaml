package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

func (s *Server) listNotifyChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.notifyStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (s *Server) createNotifyChannel(w http.ResponseWriter, r *http.Request) {
	var ch store.NotifyChannel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if ch.Name == "" || ch.WebhookURL == "" {
		writeError(w, http.StatusBadRequest, "name and webhookUrl are required")
		return
	}
	if ch.Type == "" {
		ch.Type = "feishu"
	}
	if err := s.notifyStore.Create(&ch); err != nil {
		if _, ok := err.(*store.ErrDuplicate); ok {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (s *Server) updateNotifyChannel(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	var ch store.NotifyChannel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.notifyStore.Update(name, &ch); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteNotifyChannel(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if err := s.notifyStore.Delete(name); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
