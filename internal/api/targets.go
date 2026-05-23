package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/store"
)

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.targetStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range targets {
		if targets[i].KubeconfigContent != "" {
			targets[i].KubeconfigContent = "已配置"
		}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var tgt store.K8sTarget
	if err := json.NewDecoder(r.Body).Decode(&tgt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if tgt.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if tgt.Namespace == "" {
		tgt.Namespace = "default"
	}
	if err := s.targetStore.Create(&tgt); err != nil {
		if _, ok := err.(*store.ErrDuplicate); ok {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, tgt)
}

func (s *Server) updateTarget(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	var tgt store.K8sTarget
	if err := json.NewDecoder(r.Body).Decode(&tgt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.targetStore.Update(name, &tgt); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if err := s.targetStore.Delete(name); err != nil {
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

func (s *Server) testTarget(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	// If request body has kubeconfigContent, test that directly (pre-save test).
	var body struct {
		KubeconfigContent string `json:"kubeconfigContent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var content string
	if body.KubeconfigContent != "" {
		content = body.KubeconfigContent
	} else {
		tgt, err := s.targetStore.Get(name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		content = tgt.KubeconfigContent
	}

	if content == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "kubeconfig 内容为空，请重新配置"})
		return
	}

	// Log content length for debugging.
	s.logger.Info("testing k8s connection", "contentLength", len(content), "firstChars", content[:min(100, len(content))])

	if err := s.connTester.TestK8s(content); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func min(a, b int) int {
	if a < b { return a }
	return b
}
