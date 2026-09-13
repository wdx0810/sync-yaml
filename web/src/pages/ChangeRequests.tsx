import { useEffect, useState } from 'react';
import { Tabs, Select, Input, Button, Space, Tag, Table, Modal, message, Card, Form } from 'antd';
import { EditOutlined } from '@ant-design/icons';
import ReactDiffViewer from 'react-diff-viewer-continued';
import { api } from '../api/client';
import type { SyncTask, ChangeRequest } from '../api/client';
import { buildEnvOptions } from '../utils/taskEnv';
import { diffRenderContent } from '../utils/trailingSpace';
import { formatYaml } from '../utils/yamlFormat';

const statusMeta: Record<string, { color: string; label: string }> = {
  pending: { color: 'orange', label: '待审核' },
  approved: { color: 'green', label: '已批准(已提交GitLab)' },
  rejected: { color: 'red', label: '已驳回' },
  conflict: { color: 'volcano', label: '已失效(冲突)' },
};

// ---- Submit a new change request ----
function SubmitChange({ onSubmitted }: { onSubmitted: () => void }) {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [taskId, setTaskId] = useState<string>('');
  const [configMaps, setConfigMaps] = useState<{ namespace: string; name: string; path: string }[]>([]);
  const [selected, setSelected] = useState<string>(''); // "namespace/name"
  const [content, setContent] = useState<string>('');   // current (saved) content — formatted
  const [original, setOriginal] = useState<string>(''); // formatted baseline (for change detection)
  const [rawContent, setRawContent] = useState<string>(''); // untouched GitLab raw content
  const [baseVersion, setBaseVersion] = useState<string>(''); // hash captured at load (optimistic lock)
  const [reason, setReason] = useState<string>('');
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<string>('');        // working copy while editing
  const [showRaw, setShowRaw] = useState(false);          // toggle: view formatted vs original raw
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
    setRawContent('');
    setShowRaw(false);
    setEditing(false);
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
    setRawContent('');
    setShowRaw(false);
    setEditing(false);
    const [ns, name] = v.split('|');
    setLoadingFile(true);
    const hide = message.loading('正在加载 YAML 内容...', 0);
    api.loadChangeRequestFile(taskId, ns, name)
      .then(res => {
        const raw = res.data.content;
        // Format for readable multi-line display; falls back to raw if unparseable.
        const { text: formatted, ok } = formatYaml(raw);
        const display = ok ? formatted : raw;
        setRawContent(raw);
        setContent(display);
        setOriginal(display);
        setBaseVersion(res.data.baseVersion || '');
      })
      .catch((e: any) => message.error(e.message || '加载失败'))
      .finally(() => { hide(); setLoadingFile(false); });
  };

  const startEdit = () => { setDraft(content); setEditing(true); };
  const cancelEdit = () => { setEditing(false); setDraft(''); };
  const saveEdit = () => {
    if (!draft.trim()) { message.warning('内容不能为空'); return; }
    setContent(draft);
    setEditing(false);
    if (draft === original) {
      message.info('内容与原始一致，未产生变更');
    } else {
      message.success('已保存修改（尚未提交审核）');
    }
  };

  // Actually create the change request and reset the form.
  const doSubmit = async (ns: string, name: string) => {
    setSubmitting(true);
    try {
      await api.createChangeRequest({ taskId, namespace: ns, name, newYaml: content, reason, baseVersion });
      message.success('已提交，等待审核');
      setSelected(''); setContent(''); setOriginal(''); setBaseVersion(''); setReason(''); setEditing(false);
      onSubmitted();
    } catch (e: any) {
      message.error(e.message || '提交失败');
    } finally {
      setSubmitting(false);
    }
  };

  const handleSubmit = async () => {
    if (!taskId) { message.warning('请选择环境'); return; }
    if (!selected) { message.warning('请选择 ConfigMap'); return; }
    if (editing) { message.warning('请先保存或取消当前编辑'); return; }
    if (!content.trim()) { message.warning('内容不能为空'); return; }
    if (content === original) { message.warning('内容未修改'); return; }
    if (!reason.trim()) { message.warning('请填写变更说明'); return; }
    const [ns, name] = selected.split('|');

    // Pre-check: does this ConfigMap already have pending change requests?
    // If so, pop a confirm dialog so the user decides whether to proceed.
    try {
      const res = await api.listChangeRequests('pending');
      const dup = (res.data || []).filter(
        (r) => r.taskId === taskId && r.namespace === ns && r.name === name
      );
      if (dup.length > 0) {
        const who = Array.from(new Set(dup.map((r) => r.requester))).join('、');
        Modal.confirm({
          title: '该配置已有待审核的变更',
          width: 520,
          content: (
            <div>
              <p>
                <b>{ns}/{name}</b> 当前已有 <b style={{ color: '#d46b08' }}>{dup.length}</b> 条待审核申请
                （申请人：{who}）。
              </p>
              <p style={{ color: '#64748b', margin: 0 }}>
                多人基于同一版本修改可能产生冲突：先批准的会写入 GitLab，后批准的若基线过期将被系统标记为“已失效(冲突)”并需重新提交。
              </p>
              <p style={{ marginTop: 8 }}>是否仍要提交本次变更？</p>
            </div>
          ),
          okText: '仍要提交',
          cancelText: '取消',
          onOk: () => doSubmit(ns, name),
        });
        return;
      }
    } catch {
      // If the pre-check fails, fall through and submit normally.
    }

    doSubmit(ns, name);
  };

  const changed = content !== original && original !== '';

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select
          placeholder="选择环境(GitLab 路径)"
          value={taskId || undefined}
          onChange={onTaskChange}
          style={{ width: 320 }}
          showSearch
          optionFilterProp="label"
          options={buildEnvOptions(tasks)}
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
        <Card
          size="small"
          title={editing ? '编辑 YAML 内容（已格式化）' : (showRaw ? 'YAML 内容（GitLab 原文，只读）' : 'YAML 内容（已格式化，只读）')}
          style={{ marginBottom: 12 }}
          loading={loadingFile}
          extra={
            editing ? (
              <Space>
                <Button size="small" onClick={cancelEdit}>取消</Button>
                <Button size="small" type="primary" onClick={saveEdit}>保存修改</Button>
              </Space>
            ) : (
              <Space>
                <Button size="small" onClick={() => setShowRaw(s => !s)}>
                  {showRaw ? '查看格式化' : '查看原文'}
                </Button>
                <Button size="small" icon={<EditOutlined />} onClick={startEdit}>编辑</Button>
              </Space>
            )
          }
        >
          {!editing && rawContent && content !== rawContent && !showRaw && (
            <div style={{ color: '#64748b', fontSize: 12, marginBottom: 6 }}>
              已格式化为多行显示（去除行尾空格）。提交后 GitLab 将保存为此规整格式；点“查看原文”可对照原始内容。
            </div>
          )}
          <Input.TextArea
            value={editing ? draft : (showRaw ? rawContent : content)}
            onChange={(e) => setDraft(e.target.value)}
            readOnly={!editing}
            autoSize={{ minRows: 16, maxRows: 36 }}
            style={{ fontFamily: 'monospace', fontSize: 13, background: editing ? undefined : '#f8fafc' }}
          />
        </Card>
      )}

      {changed && !editing && (
        <Card size="small" title="变更预览（左：当前 GitLab 内容，右：修改后）" style={{ marginBottom: 12 }}>
          <div style={{ maxHeight: 400, overflow: 'auto' }}>
            <ReactDiffViewer oldValue={original} newValue={content} splitView leftTitle="当前" rightTitle="修改后" useDarkTheme={false} renderContent={diffRenderContent} />
          </div>
        </Card>
      )}

      {selected && (
        <Form layout="vertical">
          <Form.Item label="变更说明" required>
            <Input.TextArea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} placeholder="请说明本次修改的目的" />
          </Form.Item>
          <Button type="primary" loading={submitting} disabled={editing || !changed} onClick={handleSubmit}>提交审核</Button>
          {editing && <span style={{ marginLeft: 12, color: '#f59e0b' }}>请先保存或取消编辑</span>}
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
  const [nameFilter, setNameFilter] = useState<string>('');
  const [taskFilter, setTaskFilter] = useState<string>('');
  const [requesterFilter, setRequesterFilter] = useState<string>('');
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
    const hide = message.loading('正在校验版本并提交到 GitLab...', 0);
    try {
      await api.approveChangeRequest(detail.id, note);
      hide();
      message.success('已批准并提交到 GitLab');
      setDetail(null); setNote('');
      fetchData();
    } catch (e: any) {
      hide();
      // 409 = optimistic-lock conflict: the file changed since this request's base.
      if (e.status === 409) {
        setDetail(null); setNote('');
        fetchData();
        Modal.warning({
          title: '版本冲突，提交失败',
          width: 520,
          content: (
            <div>
              <p style={{ margin: 0 }}>{e.message || '该文件已被其他变更更新，当前版本与申请基线不一致。'}</p>
              <p style={{ color: '#64748b', marginTop: 8, marginBottom: 0 }}>
                本申请已标记为“已失效(冲突)”。可在列表中点“查看”对照 GitLab 最新内容，再通知申请人基于最新内容重新提交。
              </p>
            </div>
          ),
          okText: '知道了',
        });
      } else {
        message.error(e.message || '操作失败');
      }
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

  // File paths that have more than one pending request — flag for reviewer awareness.
  const pendingCountByFile: Record<string, number> = {};
  for (const r of requests) {
    if (r.status === 'pending') pendingCountByFile[r.filePath] = (pendingCountByFile[r.filePath] || 0) + 1;
  }

  // Distinct values for the task and requester dropdowns.
  const taskOptions = Array.from(new Set(requests.map(r => r.taskName))).sort()
    .map(t => ({ label: t, value: t }));
  const requesterOptions = Array.from(new Set(requests.map(r => r.requester))).sort()
    .map(u => ({ label: u, value: u }));

  // Client-side combined filter: environment(task) + ConfigMap name + requester.
  const filteredRequests = requests.filter(r => {
    if (taskFilter && r.taskName !== taskFilter) return false;
    if (requesterFilter && r.requester !== requesterFilter) return false;
    if (nameFilter.trim() && !`${r.namespace}/${r.name}`.toLowerCase().includes(nameFilter.trim().toLowerCase())) return false;
    return true;
  });

  const columns = [
    { title: '环境(任务)', dataIndex: 'taskName', width: 160 },
    {
      title: 'ConfigMap', width: 240, render: (_: any, r: ChangeRequest) => (
        <Space size={4}>
          <span>{r.namespace}/{r.name}</span>
          {r.status === 'pending' && pendingCountByFile[r.filePath] > 1 && (
            <Tag color="gold">同文件{pendingCountByFile[r.filePath]}条待审</Tag>
          )}
        </Space>
      ),
    },
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
      <Space style={{ marginBottom: 12 }} wrap>
        <Select
          placeholder="全部状态"
          allowClear
          value={statusFilter || undefined}
          onChange={(v) => setStatusFilter(v || '')}
          style={{ width: 150 }}
          options={[
            { label: '待审核', value: 'pending' },
            { label: '已批准', value: 'approved' },
            { label: '已驳回', value: 'rejected' },
            { label: '已失效(冲突)', value: 'conflict' },
          ]}
        />
        <Select
          placeholder="按环境(任务)筛选"
          allowClear
          showSearch
          optionFilterProp="label"
          value={taskFilter || undefined}
          onChange={(v) => setTaskFilter(v || '')}
          style={{ width: 200 }}
          options={taskOptions}
        />
        <Select
          placeholder="按申请人筛选"
          allowClear
          showSearch
          optionFilterProp="label"
          value={requesterFilter || undefined}
          onChange={(v) => setRequesterFilter(v || '')}
          style={{ width: 160 }}
          options={requesterOptions}
        />
        <Input.Search
          placeholder="按 ConfigMap 名称筛选"
          allowClear
          value={nameFilter}
          onChange={(e) => setNameFilter(e.target.value)}
          style={{ width: 240 }}
        />
        <Button onClick={fetchData}>刷新</Button>
      </Space>
      <Table columns={columns} dataSource={filteredRequests} rowKey="id" loading={loading} size="small" />

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

            {detail.status === 'conflict' && (
              <div style={{ background: '#fff7e6', border: '1px solid #ffd591', borderRadius: 6, padding: 10, marginBottom: 12 }}>
                <p style={{ color: '#d46b08', margin: 0 }}>
                  <b>版本冲突：</b>该申请提交后，GitLab 中该文件已被其他变更更新，申请基线已过期，无法应用。
                  下方展示「申请时的基线」与「GitLab 最新内容」的差异，请申请人基于最新内容重新提交。
                </p>
              </div>
            )}

            {detail.status === 'conflict' ? (
              <>
                <p style={{ marginBottom: 4 }}><b>基线 vs GitLab 最新（他人已合入的变更）：</b></p>
                <div style={{ maxHeight: 300, overflow: 'auto', border: '1px solid #eee', borderRadius: 6, marginBottom: 12 }}>
                  <ReactDiffViewer oldValue={detail.oldYaml} newValue={detail.conflictYaml || ''} splitView leftTitle="申请时基线" rightTitle="GitLab 最新" useDarkTheme={false} renderContent={diffRenderContent} />
                </div>
                <p style={{ marginBottom: 4 }}><b>本申请原本的修改（基线 → 申请修改后）：</b></p>
                <div style={{ maxHeight: 300, overflow: 'auto', border: '1px solid #eee', borderRadius: 6 }}>
                  <ReactDiffViewer oldValue={detail.oldYaml} newValue={detail.newYaml} splitView leftTitle="申请时基线" rightTitle="申请修改后" useDarkTheme={false} renderContent={diffRenderContent} />
                </div>
              </>
            ) : (
              <div style={{ maxHeight: 420, overflow: 'auto', border: '1px solid #eee', borderRadius: 6 }}>
                <ReactDiffViewer oldValue={detail.oldYaml} newValue={detail.newYaml} splitView leftTitle="当前 GitLab" rightTitle="申请修改后" useDarkTheme={false} renderContent={diffRenderContent} />
              </div>
            )}
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
