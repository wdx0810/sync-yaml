import { useEffect, useState } from 'react';
import { Tabs, Select, Input, Button, Space, Tag, Table, Modal, message, Card, Form } from 'antd';
import ReactDiffViewer from 'react-diff-viewer-continued';
import { api } from '../api/client';
import type { SyncTask, ChangeRequest } from '../api/client';

const statusMeta: Record<string, { color: string; label: string }> = {
  pending: { color: 'orange', label: '待审核' },
  approved: { color: 'green', label: '已批准(已提交GitLab)' },
  rejected: { color: 'red', label: '已驳回' },
};

// ---- Submit a new change request ----
function SubmitChange({ onSubmitted }: { onSubmitted: () => void }) {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [taskId, setTaskId] = useState<string>('');
  const [configMaps, setConfigMaps] = useState<{ namespace: string; name: string; path: string }[]>([]);
  const [selected, setSelected] = useState<string>(''); // "namespace/name"
  const [content, setContent] = useState<string>('');
  const [original, setOriginal] = useState<string>('');
  const [reason, setReason] = useState<string>('');
  const [loadingCM, setLoadingCM] = useState(false);
  const [loadingFile, setLoadingFile] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    api.getTasks().then(res => setTasks(res.data || [])).catch(() => {});
  }, []);

  const onTaskChange = (v: string) => {
    setTaskId(v);
    setSelected('');
    setContent('');
    setOriginal('');
    setConfigMaps([]);
    setLoadingCM(true);
    const hide = message.loading('正在加载 ConfigMap 列表...', 0);
    api.listChangeRequestConfigMaps(v)
      .then(res => setConfigMaps(res.data || []))
      .catch((e: any) => message.error(e.message || '加载失败'))
      .finally(() => { hide(); setLoadingCM(false); });
  };

  const onCMChange = (v: string) => {
    setSelected(v);
    setContent('');
    setOriginal('');
    const [ns, name] = v.split('|');
    setLoadingFile(true);
    const hide = message.loading('正在加载 YAML 内容...', 0);
    api.loadChangeRequestFile(taskId, ns, name)
      .then(res => { setContent(res.data.content); setOriginal(res.data.content); })
      .catch((e: any) => message.error(e.message || '加载失败'))
      .finally(() => { hide(); setLoadingFile(false); });
  };

  const handleSubmit = async () => {
    if (!taskId) { message.warning('请选择环境(任务)'); return; }
    if (!selected) { message.warning('请选择 ConfigMap'); return; }
    if (!content.trim()) { message.warning('内容不能为空'); return; }
    if (content === original) { message.warning('内容未修改'); return; }
    if (!reason.trim()) { message.warning('请填写变更说明'); return; }
    const [ns, name] = selected.split('|');
    setSubmitting(true);
    try {
      await api.createChangeRequest({ taskId, namespace: ns, name, newYaml: content, reason });
      message.success('已提交，等待审核');
      setSelected(''); setContent(''); setOriginal(''); setReason('');
      onSubmitted();
    } catch (e: any) {
      message.error(e.message || '提交失败');
    } finally {
      setSubmitting(false);
    }
  };

  const changed = content !== original && original !== '';

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select
          placeholder="选择环境(同步任务)"
          value={taskId || undefined}
          onChange={onTaskChange}
          style={{ width: 280 }}
          options={tasks.map(t => ({ label: `${t.name} (${t.direction === 'reverse' ? 'K8s→Git' : 'Git→K8s'})`, value: t.id }))}
        />
        <Select
          placeholder="选择 ConfigMap"
          value={selected || undefined}
          onChange={onCMChange}
          loading={loadingCM}
          disabled={!taskId}
          style={{ width: 320 }}
          showSearch
          optionFilterProp="label"
          options={configMaps.map(c => ({ label: `${c.namespace} / ${c.name}`, value: `${c.namespace}|${c.name}` }))}
        />
      </Space>

      {selected && (
        <Card size="small" title="编辑 YAML 内容" style={{ marginBottom: 12 }} loading={loadingFile}>
          <Input.TextArea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            autoSize={{ minRows: 16, maxRows: 36 }}
            style={{ fontFamily: 'monospace', fontSize: 13 }}
          />
        </Card>
      )}

      {changed && (
        <Card size="small" title="变更预览（左：当前 GitLab 内容，右：修改后）" style={{ marginBottom: 12 }}>
          <div style={{ maxHeight: 400, overflow: 'auto' }}>
            <ReactDiffViewer oldValue={original} newValue={content} splitView leftTitle="当前" rightTitle="修改后" useDarkTheme={false} />
          </div>
        </Card>
      )}

      {selected && (
        <Form layout="vertical">
          <Form.Item label="变更说明" required>
            <Input.TextArea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} placeholder="请说明本次修改的目的" />
          </Form.Item>
          <Button type="primary" loading={submitting} onClick={handleSubmit}>提交审核</Button>
        </Form>
      )}
    </div>
  );
}

// ---- Review list ----
function ReviewList({ refreshKey }: { refreshKey: number }) {
  const [requests, setRequests] = useState<ChangeRequest[]>([]);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [detail, setDetail] = useState<ChangeRequest | null>(null);
  const [note, setNote] = useState('');
  const [acting, setActing] = useState(false);
  const role = localStorage.getItem('role') || '';

  const fetchData = () => {
    setLoading(true);
    api.listChangeRequests(statusFilter || undefined)
      .then(res => setRequests(res.data || []))
      .catch((e: any) => message.error(e.message || '加载失败'))
      .finally(() => setLoading(false));
  };

  useEffect(fetchData, [statusFilter, refreshKey]);

  const handleApprove = async () => {
    if (!detail) return;
    setActing(true);
    const hide = message.loading('正在提交到 GitLab...', 0);
    try {
      await api.approveChangeRequest(detail.id, note);
      hide();
      message.success('已批准并提交到 GitLab');
      setDetail(null); setNote('');
      fetchData();
    } catch (e: any) {
      hide();
      message.error(e.message || '操作失败');
    } finally {
      setActing(false);
    }
  };

  const handleReject = async () => {
    if (!detail) return;
    setActing(true);
    try {
      await api.rejectChangeRequest(detail.id, note);
      message.success('已驳回');
      setDetail(null); setNote('');
      fetchData();
    } catch (e: any) {
      message.error(e.message || '操作失败');
    } finally {
      setActing(false);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteChangeRequest(id);
      message.success('已删除');
      fetchData();
    } catch (e: any) {
      message.error(e.message || '删除失败');
    }
  };

  const columns = [
    { title: '环境(任务)', dataIndex: 'taskName', width: 160 },
    { title: 'ConfigMap', width: 200, render: (_: any, r: ChangeRequest) => `${r.namespace}/${r.name}` },
    { title: '申请人', dataIndex: 'requester', width: 110 },
    { title: '说明', dataIndex: 'reason', ellipsis: true },
    {
      title: '状态', dataIndex: 'status', width: 160,
      render: (s: string) => <Tag color={statusMeta[s]?.color || 'default'}>{statusMeta[s]?.label || s}</Tag>,
    },
    { title: '提交时间', dataIndex: 'createdAt', width: 170, render: (t: string) => new Date(t).toLocaleString() },
    {
      title: '操作', width: 150, render: (_: any, r: ChangeRequest) => (
        <Space>
          <a onClick={() => { setDetail(r); setNote(''); }}>查看</a>
          {(role === 'admin' || r.status !== 'approved') && (
            <a style={{ color: '#ef4444' }} onClick={() => handleDelete(r.id)}>删除</a>
          )}
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Select
          placeholder="全部状态"
          allowClear
          value={statusFilter || undefined}
          onChange={(v) => setStatusFilter(v || '')}
          style={{ width: 160 }}
          options={[
            { label: '待审核', value: 'pending' },
            { label: '已批准', value: 'approved' },
            { label: '已驳回', value: 'rejected' },
          ]}
        />
        <Button onClick={fetchData}>刷新</Button>
      </Space>
      <Table columns={columns} dataSource={requests} rowKey="id" loading={loading} size="small" />

      <Modal
        open={!!detail}
        title={detail ? `变更详情 - ${detail.namespace}/${detail.name}` : ''}
        onCancel={() => { setDetail(null); setNote(''); }}
        width={900}
        footer={detail?.status === 'pending' ? [
          <Button key="reject" danger loading={acting} onClick={handleReject}>驳回</Button>,
          <Button key="approve" type="primary" loading={acting} onClick={handleApprove}>批准并提交 GitLab</Button>,
        ] : [
          <Button key="close" onClick={() => setDetail(null)}>关闭</Button>,
        ]}
      >
        {detail && (
          <div>
            <p>
              <b>环境:</b> {detail.taskName}　<b>申请人:</b> {detail.requester}　
              <b>状态:</b> <Tag color={statusMeta[detail.status]?.color}>{statusMeta[detail.status]?.label}</Tag>
            </p>
            <p><b>文件路径:</b> <code>{detail.filePath}</code></p>
            <p><b>变更说明:</b> {detail.reason || '(无)'}</p>
            {detail.reviewer && <p><b>审核人:</b> {detail.reviewer}　<b>审核备注:</b> {detail.reviewNote || '(无)'}</p>}
            {detail.commitError && <p style={{ color: '#ef4444' }}><b>上次提交错误:</b> {detail.commitError}</p>}
            <div style={{ maxHeight: 420, overflow: 'auto', border: '1px solid #eee', borderRadius: 6 }}>
              <ReactDiffViewer oldValue={detail.oldYaml} newValue={detail.newYaml} splitView leftTitle="当前 GitLab" rightTitle="申请修改后" useDarkTheme={false} />
            </div>
            {detail.status === 'pending' && (
              <Input.TextArea
                style={{ marginTop: 12 }}
                placeholder="审核备注(可选)"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                rows={2}
              />
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}

export default function ChangeRequests() {
  const [refreshKey, setRefreshKey] = useState(0);
  return (
    <div>
      <h2>配置变更</h2>
      <p style={{ color: '#64748b', marginTop: -8 }}>
        编辑 ConfigMap 并提交审核，批准后将提交到 GitLab。如需下发到 K8s，请使用对应的同步任务。
      </p>
      <Tabs
        defaultActiveKey="submit"
        items={[
          { key: 'submit', label: '提交变更', children: <SubmitChange onSubmitted={() => setRefreshKey(k => k + 1)} /> },
          { key: 'review', label: '审核列表', children: <ReviewList refreshKey={refreshKey} /> },
        ]}
      />
    </div>
  );
}
