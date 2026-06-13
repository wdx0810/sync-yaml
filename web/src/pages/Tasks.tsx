import { useEffect, useState } from 'react';
import { Table, Button, Modal, Form, Input, InputNumber, Select, Tag, Space, message, Popconfirm } from 'antd';
import { PlusOutlined, DeleteOutlined, PlayCircleOutlined, PauseCircleOutlined, SyncOutlined, LoadingOutlined, EditOutlined, ApiOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { SyncTask, GitLabSource, K8sTarget, PendingChange, SyncSummary, NotifyChannel } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';
import ResourceTypeSelector from '../components/ResourceTypeSelector';
import SyncPreviewModal from '../components/SyncPreviewModal';

const { confirm } = Modal;

export default function Tasks() {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [sources, setSources] = useState<GitLabSource[]>([]);
  const [targets, setTargets] = useState<K8sTarget[]>([]);
  const [notifyChannels, setNotifyChannels] = useState<NotifyChannel[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [syncMode, setSyncMode] = useState('manual');
  const [direction, setDirection] = useState('forward');
  const [syncingId, setSyncingId] = useState<string | null>(null);
  const [editingTask, setEditingTask] = useState<SyncTask | null>(null);
  const [projectFilter, setProjectFilter] = useState<string | undefined>(undefined);
  const [nameFilter, setNameFilter] = useState<string>('');
  const [form] = Form.useForm();

  // Forward sync approval state.
  const [previewOpen, setPreviewOpen] = useState(false);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewTaskId, setPreviewTaskId] = useState<string | null>(null);
  const [previewChanges, setPreviewChanges] = useState<PendingChange[]>([]);
  const [previewSummary, setPreviewSummary] = useState<SyncSummary | undefined>(undefined);
  const [applying, setApplying] = useState(false);

  const fetchAll = async () => {
    setLoading(true); setError(null);
    try {
      const [t, s, tg, nc] = await Promise.allSettled([api.getTasks(), api.getSources(), api.getTargets(), api.getNotifyChannels()]);
      if (t.status === 'fulfilled') setTasks(t.value.data || []);
      if (s.status === 'fulfilled') setSources(s.value.data || []);
      if (tg.status === 'fulfilled') setTargets(tg.value.data || []);
      if (nc.status === 'fulfilled') setNotifyChannels(nc.value.data || []);
      if (t.status === 'rejected') setError(t.reason);
    } catch (e) { setError(e); }
    finally { setLoading(false); }
  };
  useEffect(() => { fetchAll(); }, []);

  const handleCreate = async (values: any) => {
    try {
      if (editingTask) {
        await api.updateTask(editingTask.id, { ...editingTask, ...values });
        message.success('任务已更新');
      } else {
        await api.createTask(values);
        message.success('同步任务已创建');
      }
      setModalOpen(false); setEditingTask(null); form.resetFields(); setSyncMode('manual'); setDirection('forward'); fetchAll();
    } catch (e: any) { message.error(e.message); }
  };
  const handleDelete = async (id: string) => {
    try { await api.deleteTask(id); message.success('已删除'); fetchAll(); }
    catch (e: any) { message.error(e.message); }
  };
  const openEditTask = (record: SyncTask) => {
    setEditingTask(record);
    setDirection(record.direction || 'forward');
    setSyncMode(record.syncMode || 'manual');
    form.setFieldsValue(record);
    setModalOpen(true);
  };
  const handleStart = async (id: string) => {
    try { await api.startTask(id); message.success('已启动'); fetchAll(); }
    catch (e: any) { message.error(e.message); }
  };
  const handlePause = async (id: string) => {
    try { await api.pauseTask(id); message.success('已暂停'); fetchAll(); }
    catch (e: any) { message.error(e.message); }
  };

  const showSyncResult = (info: any) => {
    if (info?.total !== undefined) {
      const parts = [`总计 ${info.total} 个`];
      if (info.synced > 0) parts.push(`${info.synced} 个已同步`);
      if (info.skipped > 0) parts.push(`${info.skipped} 个无变更跳过`);
      if (info.failed > 0) parts.push(`${info.failed} 个失败`);
      let detail = parts.join(', ');
      if (info.syncedNames?.length > 0) detail += '\n✓ 更新: ' + info.syncedNames.slice(0, 10).join(', ');
      if (info.failedNames?.length > 0) detail += '\n✗ 失败: ' + info.failedNames.slice(0, 10).join(', ');
      if (info.errors?.length > 0) detail += '\n错误详情: ' + info.errors.slice(0, 5).join('; ');
      if (info.synced === 0 && info.failed === 0) message.info(detail, 8);
      else if (info.failed > 0) message.warning(detail, 15);
      else message.success(detail, 8);
    } else {
      message.success('同步完成');
    }
  };

  const handleSync = async (task: SyncTask) => {
    // Reverse sync (K8s → GitLab): direct commit, no preview.
    if (task.direction === 'reverse') {
      setSyncingId(task.id);
      try {
        const res = await api.syncTask(task.id);
        showSyncResult(res.data);
        fetchAll();
      } catch (e: any) {
        message.error(e.message || '同步失败');
      } finally {
        setSyncingId(null);
      }
      return;
    }

    // Forward sync (GitLab → K8s): show preview for approval.
    setSyncingId(task.id);
    setPreviewTaskId(task.id);
    setPreviewOpen(true);
    setPreviewLoading(true);
    setPreviewChanges([]);
    setPreviewSummary(undefined);
    try {
      const res = await api.previewSync(task.id);
      setPreviewChanges(res.data.changes || []);
      setPreviewSummary(res.data.summary);
    } catch (e: any) {
      message.error(e.message || '预览失败');
      setPreviewOpen(false);
    } finally {
      setPreviewLoading(false);
      setSyncingId(null);
    }
  };

  const handleApprove = async (selected: PendingChange[]) => {
    if (!previewTaskId) return;
    setApplying(true);
    try {
      const res = await api.applyChanges(previewTaskId, selected);
      showSyncResult(res.data);
      setPreviewOpen(false);
      fetchAll();
    } catch (e: any) {
      message.error(e.message || '应用失败');
    } finally {
      setApplying(false);
    }
  };

  const handleWebhookToken = async (task: SyncTask) => {
    if (task.webhookToken) {
      // Show existing token and endpoint.
      const baseUrl = window.location.origin;
      const endpoint = `${baseUrl}/api/v1/hooks/sync/${task.id}?token=${task.webhookToken}`;
      confirm({
        title: 'Webhook 接口',
        width: 600,
        content: (
          <div>
            <p style={{ marginBottom: 8 }}><b>调用地址：</b></p>
            <Input.TextArea value={`POST ${endpoint}`} rows={2} readOnly style={{ fontFamily: 'monospace', fontSize: 12 }} />
            <p style={{ marginTop: 12, marginBottom: 8 }}><b>curl 示例：</b></p>
            <Input.TextArea value={`curl -X POST "${endpoint}"`} rows={2} readOnly style={{ fontFamily: 'monospace', fontSize: 12 }} />
            <p style={{ marginTop: 12, color: '#f59e0b' }}>点击"重新生成"将废弃旧 Token</p>
          </div>
        ),
        okText: '重新生成',
        cancelText: '关闭',
        onOk: async () => {
          const res = await api.generateWebhookToken(task.id);
          message.success('Token 已重新生成');
          Modal.info({ title: '新 Token', content: <Input.TextArea value={res.data.token} rows={2} readOnly style={{ fontFamily: 'monospace' }} /> });
          fetchAll();
        },
      });
    } else {
      // Generate new token.
      try {
        const res = await api.generateWebhookToken(task.id);
        const baseUrl = window.location.origin;
        const endpoint = `${baseUrl}${res.data.endpoint}`;
        Modal.success({
          title: 'Webhook Token 已生成',
          width: 600,
          content: (
            <div>
              <p><b>调用地址：</b></p>
              <Input.TextArea value={`POST ${endpoint}`} rows={2} readOnly style={{ fontFamily: 'monospace', fontSize: 12 }} />
              <p style={{ marginTop: 12 }}><b>curl 示例：</b></p>
              <Input.TextArea value={`curl -X POST "${endpoint}"`} rows={2} readOnly style={{ fontFamily: 'monospace', fontSize: 12 }} />
              <p style={{ marginTop: 12, color: '#64748b', fontSize: 12 }}>请妥善保管 Token，关闭后不再显示完整值</p>
            </div>
          ),
        });
        fetchAll();
      } catch (e: any) {
        message.error(e.message || '生成失败');
      }
    }
  };

  // Filter tasks by project and name.
  const filteredTasks = tasks.filter(t => {
    if (projectFilter && t.project !== projectFilter) return false;
    if (nameFilter && !t.name.toLowerCase().includes(nameFilter.toLowerCase())) return false;
    return true;
  });

  const statusColor: Record<string, string> = { running: 'green', paused: 'orange', error: 'red' };

  const columns = [
    { title: '项目', dataIndex: 'project', width: 100, render: (p: string) => p || '-' },
    { title: '任务名称', dataIndex: 'name' },
    {
      title: '同步方向',
      render: (_: unknown, record: SyncTask) => {
        const isF = record.direction !== 'reverse';
        return (
          <Tag color={isF ? 'blue' : 'purple'}>
            {isF ? `${record.sourceName} → ${record.targetName}` : `${record.sourceName} → ${record.targetName}`}
          </Tag>
        );
      },
    },
    { title: '模式', dataIndex: 'syncMode', width: 80, render: (m: string) => <Tag>{m}</Tag> },
    { title: '状态', dataIndex: 'status', width: 80, render: (s: string) => <Tag color={statusColor[s] || 'default'}>{s}</Tag> },
    { title: '最近同步', dataIndex: 'lastSyncTime', width: 160, render: (t: string) => t ? new Date(t).toLocaleString() : '-' },
    { title: '结果', dataIndex: 'lastSyncResult', ellipsis: true, render: (r: string) => r || '-' },
    {
      title: '操作', width: 340, render: (_: unknown, record: SyncTask) => (
        <Space>
          {record.status !== 'running' ? (
            <Button size="small" icon={<PlayCircleOutlined />} onClick={() => handleStart(record.id)}>启动</Button>
          ) : (
            <Button size="small" icon={<PauseCircleOutlined />} onClick={() => handlePause(record.id)}>暂停</Button>
          )}
          <Button
            size="small"
            type="primary"
            icon={syncingId === record.id ? <LoadingOutlined /> : <SyncOutlined />}
            loading={syncingId === record.id}
            onClick={() => handleSync(record)}
          >
            {record.direction === 'reverse' ? '同步' : '同步（需审核）'}
          </Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEditTask(record)}>编辑</Button>
          <Button size="small" icon={<ApiOutlined />} onClick={() => handleWebhookToken(record)}>
            {record.webhookToken ? 'Webhook' : '生成Token'}
          </Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDelete(record.id)}>
            <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const isForward = direction !== 'reverse';
  const sourceLabel = isForward ? '源 (GitLab)' : '源 (K8s 集群)';
  const targetLabel = isForward ? '目标 (K8s 集群)' : '目标 (GitLab)';
  const sourceOptions = isForward
    ? sources.map(s => ({ label: `${s.name} (${s.url})`, value: s.name }))
    : targets.map(t => ({ label: `${t.name} (${t.namespace})`, value: t.name }));
  const targetOptions = isForward
    ? targets.map(t => ({ label: `${t.name} (${t.namespace})`, value: t.name }))
    : sources.map(s => ({ label: `${s.name} (${s.url})`, value: s.name }));

  const handleDirectionChange = (val: string) => {
    setDirection(val);
    form.setFieldsValue({ sourceName: undefined, targetName: undefined });
  };

  return (
    <div>
      <h2>同步任务管理</h2>
      <ErrorAlert error={error} onRetry={fetchAll} />
      <Space style={{ marginBottom: 16 }} wrap>
        <Select
          placeholder="按项目筛选"
          allowClear
          value={projectFilter}
          onChange={(v) => setProjectFilter(v)}
          style={{ width: 180 }}
          options={[...new Set(tasks.map(t => t.project).filter(Boolean))].map(p => ({ label: p, value: p }))}
        />
        <Input.Search
          placeholder="搜索任务名称"
          allowClear
          value={nameFilter}
          onChange={(e) => setNameFilter(e.target.value)}
          style={{ width: 200 }}
        />
        <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditingTask(null); form.resetFields(); setModalOpen(true); }}>创建同步任务</Button>
      </Space>
      <Table columns={columns} dataSource={filteredTasks} rowKey="id" loading={loading} size="small" scroll={{ x: 900 }} />
      <Modal title={editingTask ? '编辑同步任务' : '创建同步任务'} open={modalOpen} onCancel={() => { setModalOpen(false); setEditingTask(null); setDirection('forward'); }} onOk={() => form.submit()} okText={editingTask ? '保存' : '创建'} width={520}>
        <Form form={form} layout="vertical" onFinish={handleCreate}>
          <Form.Item name="project" label="项目分组"><Input placeholder="例如: my-project（可选，用于筛选分组）" /></Form.Item>
          <Form.Item name="name" label="任务名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="direction" label="同步方向" initialValue="forward">
            <Select onChange={handleDirectionChange} options={[
              { label: 'GitLab → K8s（正向同步）', value: 'forward' },
              { label: 'K8s → GitLab（反向同步）', value: 'reverse' },
            ]} />
          </Form.Item>
          <Form.Item name="sourceName" label={sourceLabel} rules={[{ required: true, message: `请选择${sourceLabel}` }]}>
            <Select placeholder={`选择${sourceLabel}`} options={sourceOptions} />
          </Form.Item>
          <Form.Item name="sourcePath" label="GitLab 目录路径" rules={[{ required: true, message: '请输入 YAML 文件路径' }]}>
            <Input placeholder="例如: / 或 /deploy/production" />
          </Form.Item>
          <Form.Item name="targetName" label={targetLabel} rules={[{ required: true, message: `请选择${targetLabel}` }]}>
            <Select placeholder={`选择${targetLabel}`} options={targetOptions} />
          </Form.Item>
          <Form.Item name="targetNamespace" label="K8s 命名空间" rules={[{ required: true, message: '请输入命名空间' }]} tooltip="多个命名空间用逗号分隔">
            <Input placeholder="例如: default 或 production,staging" />
          </Form.Item>
          <Form.Item name="syncMode" label="同步模式" initialValue="manual">
            <Select onChange={(v) => setSyncMode(v)} options={[
              { label: '手动 (Manual)', value: 'manual' },
              { label: '定时 (Scheduled)', value: 'scheduled' },
              { label: '自动 (Auto - Webhook)', value: 'auto' },
            ]} />
          </Form.Item>
          {syncMode === 'scheduled' && (
            <Form.Item name="interval" label="同步间隔（秒）" rules={[{ required: true }, { type: 'number', min: 30, message: '最少 30 秒' }]}>
              <InputNumber style={{ width: '100%' }} placeholder="300" />
            </Form.Item>
          )}
          <Form.Item name="resourceTypes" label="资源类型" initialValue={['All']}>
            <ResourceTypeSelector />
          </Form.Item>
          <Form.Item name="includeFilter" label="包含过滤（正则）" tooltip="只同步名称匹配的资源，为空则全部包含。例如：lion-.*">
            <Input placeholder="例如: lion-.* 或 tsp-app-.* （留空=全部）" />
          </Form.Item>
          <Form.Item name="excludeFilter" label="排除过滤（正则）" tooltip="跳过名称匹配的资源。例如：system:.*|everest-.*|cce-.*">
            <Input placeholder="例如: system:.*|everest-.*|kube-.* （留空=不排除）" />
          </Form.Item>
          <Form.Item name="notifyChannel" label="飞书通知" tooltip="反向同步完成有变更时发送通知到飞书群">
            <Select
              allowClear
              placeholder="不通知"
              options={notifyChannels.map(ch => ({ label: `${ch.name} (${ch.type})`, value: ch.name }))}
            />
          </Form.Item>
        </Form>
      </Modal>

      <SyncPreviewModal
        open={previewOpen}
        onCancel={() => setPreviewOpen(false)}
        onApprove={handleApprove}
        changes={previewChanges}
        summary={previewSummary}
        loading={previewLoading}
        applying={applying}
      />
    </div>
  );
}
