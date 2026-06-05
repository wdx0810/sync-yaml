import axios from 'axios';

const apiClient = axios.create({ baseURL: '/api/v1' });

// Add auth token to all requests.
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response) {
      const { status, data } = error.response;
      // If 401, clear token and reload to show login.
      if (status === 401) {
        localStorage.removeItem('token');
        localStorage.removeItem('username');
        window.location.reload();
        return Promise.reject({ status, message: '登录已过期', isApiError: true });
      }
      return Promise.reject({ status, message: data?.message || error.message, isApiError: true });
    }
    if (error.request) {
      // No response received. Surface the underlying axios code so the user can
      // tell timeout from connection-reset from CORS.
      const code = error.code || '';
      const detail = error.message || '';
      let hint = '请求未收到响应';
      if (code === 'ECONNABORTED' || /timeout/i.test(detail)) hint = '请求超时';
      else if (code === 'ERR_NETWORK') hint = '网络不通或被阻断';
      else if (code === 'ERR_CANCELED') hint = '请求被取消';
      return Promise.reject({
        status: 0,
        message: `${hint}（${code || 'no-response'}: ${detail}）`,
        isNetworkError: true,
      });
    }
    return Promise.reject(error);
  }
);

// ---- Types ----

export interface GitLabSource {
  name: string;
  url: string;
  token: string;
  projectId: number;
  branch: string;
  path: string;
  webhookSecret?: string;
  status: string;
  error?: string;
}

export interface K8sTarget {
  name: string;
  kubeconfigContent: string;
  namespace: string;
  status: string;
  error?: string;
}

export interface SyncTask {
  id: string;
  name: string;
  project?: string;
  sourceName: string;
  targetName: string;
  sourcePath?: string;
  targetNamespace?: string;
  direction: string;
  syncMode: string;
  interval: number;
  resourceTypes?: string[];
  includeFilter?: string;
  excludeFilter?: string;
  status: string;
  lastSyncTime: string;
  lastSyncResult: string;
  errorMessage?: string;
}

export interface DashboardSummary {
  total: number;
  running: number;
  paused: number;
  error: number;
}

export interface DashboardData {
  summary: DashboardSummary;
  tasks: SyncTask[];
}

export interface ConfigMapStatus {
  name: string;
  namespace: string;
  syncStatus: string;
  lastSyncTime: string;
}

export interface ConfigMapDetail {
  name: string;
  namespace: string;
  desiredState: Record<string, string>;
  actualState: Record<string, string>;
  diff: { field: string; expected: string; actual: string }[];
  syncStatus: string;
  lastSyncTime: string;
}

export interface DriftAlert {
  id: string;
  configMapName: string;
  namespace: string;
  diffFields: string[];
  detectedAt: string;
  status: string;
}

export interface SyncRecord {
  id: string;
  timestamp: string;
  configMapName: string;
  namespace: string;
  direction: string;
  changeType: string;
  status: string;
  beforeSummary: string;
  afterSummary: string;
  errorMessage?: string;
}

export interface HistoryFilter {
  name?: string;
  namespace?: string;
  direction?: string;
  since?: string;
  until?: string;
}

export interface TestResult {
  success: boolean;
  error?: string;
}

// ---- Users & Permissions ----

export type UserRole = 'admin' | 'user';

export interface MFASettings {
  enabled: boolean;
  secret?: string;
  configured?: boolean;
}

export interface User {
  username: string;
  password?: string;
  role: UserRole;
  enabled: boolean;
  mfa?: MFASettings;
}

export interface TaskPermission {
  taskId: string;
  canView: boolean;
  canSync: boolean;
  canEdit: boolean;
}

export interface ProjectPermission {
  project: string;
  canView: boolean;
  canSync: boolean;
  canEdit: boolean;
}

export interface UserPermissions {
  username: string;
  permissions: TaskPermission[];
  projectPermissions: ProjectPermission[];
}

// ---- Forward Sync Preview / Approval ----

export interface PendingChange {
  kind: string;
  namespace: string;
  name: string;
  action: 'created' | 'updated';
  oldYAML: string;
  newYAML: string;
  apiVersion: string;
  namespaced: boolean;
}

export interface SyncSummary {
  total: number;
  synced: number;
  failed: number;
  skipped: number;
  syncedNames?: string[];
  skippedNames?: string[];
  failedNames?: string[];
  errors?: string[];
}

export interface PreviewResult {
  direction: string;
  changes: PendingChange[];
  summary: SyncSummary;
}

// ---- API ----

export const api = {
  // Dashboard
  getDashboard: () => apiClient.get<DashboardData>('/dashboard'),

  // Sources
  getSources: () => apiClient.get<GitLabSource[]>('/sources'),
  createSource: (src: Partial<GitLabSource>) => apiClient.post<GitLabSource>('/sources', src),
  updateSource: (name: string, src: Partial<GitLabSource>) => apiClient.put(`/sources/${name}`, src),
  deleteSource: (name: string) => apiClient.delete(`/sources/${name}`),
  testSource: (name: string) => apiClient.post<TestResult>(`/sources/${name}/test`),

  // Targets
  getTargets: () => apiClient.get<K8sTarget[]>('/targets'),
  createTarget: (tgt: Partial<K8sTarget>) => apiClient.post<K8sTarget>('/targets', tgt),
  updateTarget: (name: string, tgt: Partial<K8sTarget>) => apiClient.put(`/targets/${name}`, tgt),
  deleteTarget: (name: string) => apiClient.delete(`/targets/${name}`),
  testTarget: (name: string) => apiClient.post<TestResult>(`/targets/${name}/test`),

  // Tasks
  getTasks: () => apiClient.get<SyncTask[]>('/tasks'),
  createTask: (task: Partial<SyncTask>) => apiClient.post<SyncTask>('/tasks', task),
  updateTask: (id: string, task: Partial<SyncTask>) => apiClient.put(`/tasks/${id}`, task),
  deleteTask: (id: string) => apiClient.delete(`/tasks/${id}`),
  startTask: (id: string) => apiClient.post(`/tasks/${id}/start`),
  pauseTask: (id: string) => apiClient.post(`/tasks/${id}/pause`),
  syncTask: (id: string) => apiClient.post(`/tasks/${id}/sync`),
  previewSync: (id: string) => apiClient.post<PreviewResult>(`/tasks/${id}/preview`),
  applyChanges: (id: string, changes: PendingChange[]) => apiClient.post<SyncSummary>(`/tasks/${id}/apply`, { changes }),

  // Users
  getCurrentUser: () => apiClient.get<User>('/users/me'),
  getUsers: () => apiClient.get<User[]>('/users'),
  getUser: (username: string) => apiClient.get<User>(`/users/${username}`),
  createUser: (user: User) => apiClient.post<User>('/users', user),
  updateUser: (username: string, user: Partial<User>) => apiClient.put<User>(`/users/${username}`, user),
  deleteUser: (username: string) => apiClient.delete(`/users/${username}`),
  getUserPermissions: (username: string) => apiClient.get<UserPermissions>(`/users/${username}/permissions`),
  setTaskPermission: (username: string, taskId: string, perm: Omit<TaskPermission, 'taskId'>) =>
    apiClient.put<TaskPermission>(`/users/${username}/permissions/${taskId}`, perm),
  removeTaskPermission: (username: string, taskId: string) =>
    apiClient.delete(`/users/${username}/permissions/${taskId}`),
  setProjectPermission: (username: string, project: string, perm: Omit<ProjectPermission, 'project'>) =>
    apiClient.put<ProjectPermission>(`/users/${username}/project-permissions/${encodeURIComponent(project)}`, perm),
  removeProjectPermission: (username: string, project: string) =>
    apiClient.delete(`/users/${username}/project-permissions/${encodeURIComponent(project)}`),
  resetUserMFA: (username: string) => apiClient.post(`/users/${username}/mfa-reset`),
  setUserMFAEnabled: (username: string, enabled: boolean) =>
    apiClient.put(`/users/${username}/mfa-enabled`, { enabled }),

  // MFA
  getMFAStatus: () => apiClient.get<{ enabled: boolean }>('/auth/mfa/status'),
  setupMFA: () => apiClient.post<{ secret: string; otpauthURL: string; issuer: string; account: string }>('/auth/mfa/setup'),
  enableMFA: (code: string) => apiClient.post('/auth/mfa/enable', { code }),
  disableMFA: (code: string) => apiClient.post('/auth/mfa/disable', { code }),
  verifyMFA: (username: string, code: string) => apiClient.post('/auth/mfa/verify', { username, code }),

  // Existing
  getConfigMaps: () => apiClient.get<ConfigMapStatus[]>('/configmaps'),
  getConfigMapDetail: (ns: string, name: string) => apiClient.get<ConfigMapDetail>(`/configmaps/${ns}/${name}`),
  forwardSync: () => apiClient.post('/forward-sync'),
  forwardSyncOne: (ns: string, name: string) => apiClient.post(`/forward-sync/${ns}/${name}`),
  reverseSync: (ns: string, name: string) => apiClient.post(`/reverse-sync/${ns}/${name}`),
  getDriftAlerts: () => apiClient.get<DriftAlert[]>('/drift-alerts'),
  dismissAlert: (id: string) => apiClient.post(`/drift-alerts/${id}/dismiss`),
  getHistory: (params: HistoryFilter) => apiClient.get<SyncRecord[]>('/history', { params }),
  checkGitLab: () => apiClient.post('/check-gitlab'),
};
