package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

// contentHash returns a stable hash of file content, ignoring surrounding
// whitespace so trivial formatting doesn't count as a version change.
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

// The change-request module lets developers edit a ConfigMap's YAML and submit it
// for approval. Approving commits the new content to GitLab only — it never touches
// K8s or the sync engine. Pushing to K8s is done separately via the existing sync tasks.

// resolveTaskGitLab returns the GitLab source and effective path for a task.
// For reverse tasks GitLab is the target; for forward tasks GitLab is the source.
func (s *Server) resolveTaskGitLab(taskID string) (*store.GitLabSource, *store.SyncTask, string, error) {
	task, err := s.taskStore.Get(taskID)
	if err != nil {
		return nil, nil, "", err
	}
	var source *store.GitLabSource
	if task.Direction == "reverse" {
		source, err = s.sourceStore.Get(task.TargetName)
	} else {
		source, err = s.sourceStore.Get(task.SourceName)
	}
	if err != nil {
		return nil, nil, "", err
	}
	path := source.Path
	if task.SourcePath != "" {
		path = task.SourcePath
	}
	return source, task, path, nil
}

// configMapFilePath builds the GitLab file path for a ConfigMap under the task path.
// Layout: {basePath}/{namespace}/configmaps/{name}.yaml
func configMapFilePath(basePath, namespace, name string) string {
	basePath = strings.TrimPrefix(strings.TrimSuffix(basePath, "/"), "/")
	if basePath == "" {
		return namespace + "/configmaps/" + name + ".yaml"
	}
	return basePath + "/" + namespace + "/configmaps/" + name + ".yaml"
}

// listChangeRequestConfigMaps handles GET /api/v1/change-requests/configmaps?taskId=xxx
// Returns the list of existing ConfigMap files (namespace/name) under the task path.
func (s *Server) listChangeRequestConfigMaps(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("taskId")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId 参数必填")
		return
	}
	if !s.checkTaskAccess(w, r, taskID, "view") {
		return
	}
	source, _, path, err := s.resolveTaskGitLab(taskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "GitLab 数据源未找到: "+err.Error())
		return
	}
	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	files, err := gc.ListFiles(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type cmInfo struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Path      string `json:"path"`
	}
	var result []cmInfo
	for _, fp := range files {
		// Only ConfigMap files: .../{namespace}/configmaps/{name}.yaml
		parts := strings.Split(fp, "/")
		if len(parts) < 3 {
			continue
		}
		if parts[len(parts)-2] != "configmaps" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(parts[len(parts)-1], ".yaml"), ".yml")
		ns := parts[len(parts)-3]
		result = append(result, cmInfo{Namespace: ns, Name: name, Path: fp})
	}
	writeJSON(w, http.StatusOK, result)
}

// loadChangeRequestFile handles GET /api/v1/change-requests/load-file?taskId=xxx&namespace=ns&name=cm
// Returns the current YAML content of a ConfigMap from GitLab.
func (s *Server) loadChangeRequestFile(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("taskId")
	namespace := r.URL.Query().Get("namespace")
	name := r.URL.Query().Get("name")
	if taskID == "" || namespace == "" || name == "" {
		writeError(w, http.StatusBadRequest, "taskId, namespace, name 参数必填")
		return
	}
	if !s.checkTaskAccess(w, r, taskID, "view") {
		return
	}
	source, _, path, err := s.resolveTaskGitLab(taskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "GitLab 数据源未找到: "+err.Error())
		return
	}
	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filePath := configMapFilePath(path, namespace, name)
	content, err := gc.GetFile(r.Context(), filePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "无法读取该 ConfigMap 文件: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"filePath":    filePath,
		"content":     string(content),
		"baseVersion": contentHash(string(content)),
	})
}

// listChangeRequests handles GET /api/v1/change-requests?status=pending
func (s *Server) listChangeRequests(w http.ResponseWriter, r *http.Request) {
	if s.changeReqStore == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	status := r.URL.Query().Get("status")
	reqs, err := s.changeReqStore.List(status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Non-admins only see requests for tasks they can access, plus their own.
	username := r.Header.Get("X-Username")
	if s.userStore != nil && username != "" {
		if user, err := s.userStore.GetUser(username); err == nil && user.Role != store.RoleAdmin {
			filtered := reqs[:0]
			for _, cr := range reqs {
				if cr.Requester == username {
					filtered = append(filtered, cr)
					continue
				}
				if ok, _ := s.userStore.CanAccessTask(username, cr.TaskID, cr.Project, "view"); ok {
					filtered = append(filtered, cr)
				}
			}
			reqs = filtered
		}
	}
	writeJSON(w, http.StatusOK, reqs)
}

// getChangeRequest handles GET /api/v1/change-requests/{id}
func (s *Server) getChangeRequest(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	cr, err := s.changeReqStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "变更申请不存在")
		return
	}
	writeJSON(w, http.StatusOK, cr)
}

// createChangeRequest handles POST /api/v1/change-requests
func (s *Server) createChangeRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID      string `json:"taskId"`
		Namespace   string `json:"namespace"`
		Name        string `json:"name"`
		NewYAML     string `json:"newYaml"`
		Reason      string `json:"reason"`
		BaseVersion string `json:"baseVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if body.TaskID == "" || body.Namespace == "" || body.Name == "" || strings.TrimSpace(body.NewYAML) == "" {
		writeError(w, http.StatusBadRequest, "taskId, namespace, name, newYaml 必填")
		return
	}
	// The requester needs at least view permission on the task to submit a change.
	if !s.checkTaskAccess(w, r, body.TaskID, "view") {
		return
	}

	source, task, path, err := s.resolveTaskGitLab(body.TaskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "GitLab 数据源未找到: "+err.Error())
		return
	}

	// Fetch current content as the diff base (best-effort; file may not exist yet).
	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filePath := configMapFilePath(path, body.Namespace, body.Name)
	oldYAML := ""
	if content, gerr := gc.GetFile(r.Context(), filePath); gerr == nil {
		oldYAML = string(content)
	}

	// Base version for optimistic locking. Prefer the client-provided hash
	// (captured at load time); fall back to hashing what we just fetched.
	baseVersion := body.BaseVersion
	if baseVersion == "" {
		baseVersion = contentHash(oldYAML)
	}

	// Warn (do not block) if there are other pending requests for the same file.
	var samePending []string
	if all, err := s.changeReqStore.List(store.ChangeRequestPending); err == nil {
		for _, cr := range all {
			if cr.FilePath == filePath {
				samePending = append(samePending, cr.Requester)
			}
		}
	}

	username := r.Header.Get("X-Username")
	cr := &store.ChangeRequest{
		TaskID:      body.TaskID,
		TaskName:    task.Name,
		Project:     task.Project,
		Namespace:   body.Namespace,
		Name:        body.Name,
		FilePath:    filePath,
		OldYAML:     oldYAML,
		NewYAML:     body.NewYAML,
		BaseVersion: baseVersion,
		Reason:      body.Reason,
		Status:      store.ChangeRequestPending,
		Requester:   username,
	}
	if err := s.changeReqStore.Create(cr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]interface{}{"request": cr}
	if len(samePending) > 0 {
		resp["warning"] = "该文件已有 " + itoa(len(samePending)) + " 条待审核申请（申请人：" +
			strings.Join(uniqueStrings(samePending), "、") + "），请审核人注意先后顺序。"
	}
	writeJSON(w, http.StatusOK, resp)
}

// itoa is a tiny int-to-string helper to avoid importing strconv for one use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	s := string(buf[i:])
	if neg {
		return "-" + s
	}
	return s
}

// uniqueStrings returns the input with duplicates removed, order preserved.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// approveChangeRequest handles POST /api/v1/change-requests/{id}/approve
// On approval the new YAML is committed to GitLab. Requires edit permission on the task.
func (s *Server) approveChangeRequest(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	cr, err := s.changeReqStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "变更申请不存在")
		return
	}
	if cr.Status != store.ChangeRequestPending {
		writeError(w, http.StatusBadRequest, "该申请已处理，无法重复审批")
		return
	}
	// Approver needs edit permission on the task.
	if !s.checkTaskAccess(w, r, cr.TaskID, "edit") {
		return
	}

	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	source, task, _, err := s.resolveTaskGitLab(cr.TaskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "GitLab 数据源未找到: "+err.Error())
		return
	}
	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Optimistic-lock check: re-read the current GitLab content and compare its
	// hash to the base version captured when the request was created. If they
	// differ, the file was changed by another commit in the meantime — reject
	// the commit, mark this request as conflicted, and stash the current content
	// so the UI can show a base-vs-latest diff.
	if cr.BaseVersion != "" {
		currentContent := ""
		if b, gerr := gc.GetFile(r.Context(), cr.FilePath); gerr == nil {
			currentContent = string(b)
		}
		if contentHash(currentContent) != cr.BaseVersion {
			cr.Status = store.ChangeRequestConflict
			cr.Reviewer = r.Header.Get("X-Username")
			cr.ReviewedAt = time.Now().Format(time.RFC3339)
			cr.ConflictYAML = currentContent
			_ = s.changeReqStore.Update(cr)
			writeError(w, http.StatusConflict,
				"提交失败：该文件已被其他变更更新（当前版本与申请基线不一致）。本申请已标记为“已失效(冲突)”，请通知申请人基于最新内容重新提交。")
			return
		}
	}

	username := r.Header.Get("X-Username")
	commitMsg := "[" + task.Name + "] 配置变更: " + cr.Namespace + "/" + cr.Name
	if cr.Reason != "" {
		commitMsg += " - " + cr.Reason
	}
	commitMsg += " (申请人: " + cr.Requester + ", 审核人: " + username + ")"

	if err := gc.CommitFile(r.Context(), cr.FilePath, []byte(cr.NewYAML), commitMsg); err != nil {
		cr.CommitError = err.Error()
		_ = s.changeReqStore.Update(cr)
		writeError(w, http.StatusInternalServerError, "提交 GitLab 失败: "+err.Error())
		return
	}

	cr.Status = store.ChangeRequestApproved
	cr.Reviewer = username
	cr.ReviewNote = body.Note
	cr.ReviewedAt = time.Now().Format(time.RFC3339)
	cr.CommitError = ""
	if err := s.changeReqStore.Update(cr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cr)
}

// rejectChangeRequest handles POST /api/v1/change-requests/{id}/reject
func (s *Server) rejectChangeRequest(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	cr, err := s.changeReqStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "变更申请不存在")
		return
	}
	if cr.Status != store.ChangeRequestPending {
		writeError(w, http.StatusBadRequest, "该申请已处理，无法重复操作")
		return
	}
	if !s.checkTaskAccess(w, r, cr.TaskID, "edit") {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	cr.Status = store.ChangeRequestRejected
	cr.Reviewer = r.Header.Get("X-Username")
	cr.ReviewNote = body.Note
	cr.ReviewedAt = time.Now().Format(time.RFC3339)
	if err := s.changeReqStore.Update(cr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cr)
}

// deleteChangeRequest handles DELETE /api/v1/change-requests/{id}
func (s *Server) deleteChangeRequest(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	cr, err := s.changeReqStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "变更申请不存在")
		return
	}
	// Requester can delete their own; otherwise needs edit permission.
	username := r.Header.Get("X-Username")
	if cr.Requester != username {
		if !s.checkTaskAccess(w, r, cr.TaskID, "edit") {
			return
		}
	}
	if err := s.changeReqStore.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
