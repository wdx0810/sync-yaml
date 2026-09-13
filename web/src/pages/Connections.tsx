import { useEffect, useState } from 'react';
import { Tabs, Table, Button, Modal, Form, Input, InputNumber, Tag, Space, message, Popconfirm, Switch } from 'antd';
import { PlusOutlined, DeleteOutlined, CheckCircleOutlined, EditOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { GitLabSource, K8sTarget, NotifyChannel } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function Connections() {
  const [sources, setSources] = useState<GitLabSource[]>([]);
  const [targets, setTargets] = useState<K8sTarget[]>([]);
  const [notifyChannels, setNotifyChannels] = useState<NotifyChannel[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [gitlabModalOpen, setGitlabModalOpen] = useState(false);
  const [k8sModalOpen, setK8sModalOpen] = useState(false);
  const [notifyModalOpen, setNotifyModalOpen] = useState(false);
  const [editingGitlab, setEditingGitlab] = useState<string | null>(null);
  const [editingK8s, setEditingK8s] = useState<string | null>(null);
  const [editingNotify, setEditingNotify] = useState<string | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [gitlabForm] = Form.useForm();
  const [k8sForm] = Form.useForm();
  const [notifyForm] = Form.useForm();
  const [feishuForm] = Form.useForm();
  const [savingFeishu, setSavingFeishu] = useState(false);

  const fetchAll = async () => {
    setLoading(true); setError(null);
    try {
      const [s, t, n] = await Promise.all([api.getSources(), api.getTargets(), api.getNotifyChannels()]);
      setSources(s.data || []); setTargets(t.data || []); setNotifyChannels(n.data || []);
    } catch (e) { setError(e); }
    finally { setLoading(false); }
  };
  useEffect(() => { fetchAll(); }, []);

  // Load Feishu config into its form.
  useEffect(() => {
    api.getFeishuConfig().then(res => feishuForm.setFieldsValue(res.data)).catch(() => {});
  }, [feishuForm]);

  const handleFeishuSave = async (values: any) => {
    setSavingFeishu(true);
    try {
      await api.saveFeishuConfig(values);
      message.success('飞书配置已保存');
      api.getFeishuConfig().then(res => feishuForm.setFieldsValue(res.data)).catch(() => {});
    } catch (e: any) { message.error(e.message || '保存失败'); }
    finally { setSavingFeishu(false); }
  };

  // GitLab
  const handleGitlabSubmit = async (values: any) => {
    try {
      if (editingGitlab) {
        await api.updateSource(editingGitlab, values);
        message.success('已更新');
      } else {
        await api.createSource(values);
        message.success('已添加');
      }
      setGitlabModalOpen(false); setEditingGitlab(null); gitlabForm.resetFields(); fetchAll();
    } catch (e: any) { message.error(e.message); }
  };
  const openEditGitlab = (r: GitLabSource) => {
    setEditingGitlab(r.name);
    gitlabForm.setFieldsValue({ ...r, token: '' }); // don't prefill masked token
    setGitlabModalOpen(true);
  };
  const handleDeleteGitlab = async (name: string) => {
    try { await api.deleteSource(name); message.success('已删除'); fetchAll(); }
    catch (e: any) { message.error(e.message); }
  };
  const handleTestGitlab = async (name: string) => {
    setTesting(`gl-${name}`);
    try {
      const res = await api.testSource(name);
      res.data.success ? message.success('连接成功') : message.error(`失败: ${res.data.error}`);
    } catch (e: any) { message.error(e.message); }
    finally { setTesting(null); }
  };

  // K8s
  const handleK8sSubmit = async (values: any) => {
    try {
      if (editingK8s) {
        await api.updateTarget(editingK8s, values);
        message.success('已更新');
      } else {
        await api.createTarget(values);
        message.success('已添加');
      }
      setK8sModalOpen(false); setEditingK8s(null); k8sForm.resetFields(); fetchAll();
    } catch (e: any) { message.error(e.message); }
  };
  const openEditK8s = (r: K8sTarget) => {
    setEditingK8s(r.name);
    k8sForm.setFieldsValue({ ...r, kubeconfigContent: '' }); // don't prefill masked content
    setK8sModalOpen(true);
  };
  const handleDeleteK8s = async (name: string) => {
    try { await api.deleteTarget(name); message.success('已删除'); fetchAll(); }
    catch (e: any) { message.error(e.message); }
  };
  const handleTestK8s = async (name: string) => {
    setTesting(`k8s-${name}`);
    try {
      const res = await api.testTarget(name);
      res.data.success ? message.success('连接成功') : message.error(`失败: ${res.data.error}`);
    } catch (e: any) { message.error(e.message); }
    finally { setTesting(null); }
  };

  const statusTag = (s: string) => <Tag color={s === 'connected' ? 'green' : s === 'error' ? 'red' : 'default'}>{s}</Tag>;

  const gitlabColumns = [
    { title: '名称', dataIndex: 'name' },
    { title: 'URL', dataIndex: 'url' },
    { title: 'Project ID', dataIndex: 'projectId' },
    { title: '分支', dataIndex: 'branch' },
    { title: '状态', dataIndex: 'status', render: statusTag },
    {
      title: '操作', render: (_: unknown, r: GitLabSource) => (
        <Space>
          <Button size="small" icon={<CheckCircleOutlined />} loading={testing === `gl-${r.name}`} onClick={() => handleTestGitlab(r.name)}>测试</Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEditGitlab(r)}>编辑</Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDeleteGitlab(r.name)}>
            <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const k8sColumns = [
    { title: '名称', dataIndex: 'name' },
    { title: 'Kubeconfig', dataIndex: 'kubeconfigContent', render: (v: string) => v ? '(已配置)' : '(未配置)' },
    { title: '状态', dataIndex: 'status', render: statusTag },
    {
      title: '操作', render: (_: unknown, r: K8sTarget) => (
        <Space>
          <Button size="small" icon={<CheckCircleOutlined />} loading={testing === `k8s-${r.name}`} onClick={() => handleTestK8s(r.name)}>测试</Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEditK8s(r)}>编辑</Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDeleteK8s(r.name)}>
            <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <h2>数据源管理</h2>
      <ErrorAlert error={error} onRetry={fetchAll} />
      <Tabs items={[
        {
          key: 'gitlab', label: `GitLab (${sources.length})`,
          children: (
            <>
              <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditingGitlab(null); gitlabForm.resetFields(); setGitlabModalOpen(true); }} style={{ marginBottom: 16 }}>添加 GitLab</Button>
              <Table columns={gitlabColumns} dataSource={sources} rowKey="name" loading={loading} />
            </>
          ),
        },
        {
          key: 'k8s', label: `K8s 集群 (${targets.length})`,
          children: (
            <>
              <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditingK8s(null); k8sForm.resetFields(); setK8sModalOpen(true); }} style={{ marginBottom: 16 }}>添加 K8s 集群</Button>
              <Table columns={k8sColumns} dataSource={targets} rowKey="name" loading={loading} />
            </>
          ),
        },
        {
          key: 'notify', label: `通知渠道 (${notifyChannels.length})`,
          children: (
            <>
              <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditingNotify(null); notifyForm.resetFields(); notifyForm.setFieldsValue({ type: 'feishu' }); setNotifyModalOpen(true); }} style={{ marginBottom: 16 }}>添加通知渠道</Button>
              <Table
                columns={[
                  { title: '名称', dataIndex: 'name' },
                  { title: '类型', dataIndex: 'type', render: (t: string) => <Tag color="blue">{t === 'feishu' ? '飞书' : t}</Tag> },
                  { title: 'Webhook URL', dataIndex: 'webhookUrl', ellipsis: true },
                  {
                    title: '操作', render: (_: unknown, r: NotifyChannel) => (
                      <Space>
                        <Button size="small" icon={<EditOutlined />} onClick={() => { setEditingNotify(r.name); notifyForm.setFieldsValue(r); setNotifyModalOpen(true); }}>编辑</Button>
                        <Popconfirm title="确认删除？" onConfirm={async () => { try { await api.deleteNotifyChannel(r.name); message.success('已删除'); fetchAll(); } catch (e: any) { message.error(e.message); } }}>
                          <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  },
                ]}
                dataSource={notifyChannels}
                rowKey="name"
                loading={loading}
              />
            </>
          ),
        },
        {
          key: 'feishu', label: '飞书集成',
          children: (
            <div style={{ maxWidth: 640 }}>
              <p style={{ color: '#64748b' }}>
                配置飞书企业自建应用后，本企业成员可在登录页扫码登录（首次登录自动创建无权限账号，由管理员分配权限）；
                配置变更提交/驳回时会通过飞书通知相关人员。
              </p>
              <Form form={feishuForm} layout="vertical" onFinish={handleFeishuSave}>
                <Form.Item name="enabled" label="启用飞书登录" valuePropName="checked" initialValue={false}>
                  <Switch />
                </Form.Item>
                <Form.Item name="appId" label="App ID" rules={[{ required: true }]}>
                  <Input placeholder="cli_xxxxxxxx" />
                </Form.Item>
                <Form.Item name="appSecret" label="App Secret" tooltip="已保存的密钥显示为 ***，留空或 *** 表示不修改">
                  <Input.Password placeholder="留空表示不修改" />
                </Form.Item>
                <Form.Item name="redirectUri" label="重定向 URL(回调地址)" rules={[{ required: true }]}
                  tooltip="需与飞书开放平台里配置的重定向 URL 完全一致">
                  <Input placeholder={`${window.location.origin}/api/v1/auth/feishu/callback`} />
                </Form.Item>
                <div style={{ color: '#64748b', fontSize: 12, marginBottom: 12 }}>
                  提示：在飞书开放平台「安全设置 - 重定向 URL」中填写：
                  <code style={{ marginLeft: 4 }}>{window.location.origin}/api/v1/auth/feishu/callback</code>
                  ，并开通 用户信息、im:message 等权限。
                </div>
                <Button type="primary" htmlType="submit" loading={savingFeishu}>保存飞书配置</Button>
              </Form>
            </div>
          ),
        },
      ]} />

      <Modal title={editingGitlab ? '编辑 GitLab' : '添加 GitLab'} open={gitlabModalOpen} onCancel={() => { setGitlabModalOpen(false); setEditingGitlab(null); }} onOk={() => gitlabForm.submit()} okText={editingGitlab ? '保存' : '添加'}>
        <Form form={gitlabForm} layout="vertical" onFinish={handleGitlabSubmit}>
          <Form.Item name="name" label="名称" rules={[{ required: !editingGitlab }]}><Input disabled={!!editingGitlab} /></Form.Item>
          <Form.Item name="url" label="GitLab URL" rules={[{ required: true }]}><Input placeholder="https://gitlab.example.com" /></Form.Item>
          <Form.Item name="token" label="Access Token" rules={[{ required: !editingGitlab }]}><Input.Password placeholder={editingGitlab ? '留空则不修改' : ''} /></Form.Item>
          <Form.Item name="projectId" label="Project ID" rules={[{ required: true }]}><InputNumber style={{ width: '100%' }} /></Form.Item>
          <Form.Item name="branch" label="分支" initialValue="main"><Input /></Form.Item>
          <Form.Item name="webhookSecret" label="Webhook Secret"><Input.Password /></Form.Item>
        </Form>
      </Modal>

      <Modal title={editingK8s ? '编辑 K8s 集群' : '添加 K8s 集群'} open={k8sModalOpen} onCancel={() => { setK8sModalOpen(false); setEditingK8s(null); }} onOk={() => k8sForm.submit()} okText={editingK8s ? '保存' : '添加'}>
        <Form form={k8sForm} layout="vertical" onFinish={handleK8sSubmit}>
          <Form.Item name="name" label="名称" rules={[{ required: !editingK8s }]}><Input disabled={!!editingK8s} /></Form.Item>
          <Form.Item name="kubeconfigContent" label="Kubeconfig 内容">
            <Input.TextArea rows={8} placeholder={editingK8s ? '留空则不修改' : '粘贴 kubeconfig YAML 内容'} style={{ fontFamily: 'monospace', fontSize: 12 }} />
          </Form.Item>
          <Form.Item label="或上传 kubeconfig 文件">
            <input type="file" accept=".yaml,.yml,.conf,*" onChange={async (e) => {
              const file = e.target.files?.[0];
              if (file) {
                const text = await file.text();
                k8sForm.setFieldsValue({ kubeconfigContent: text });
              }
            }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal title={editingNotify ? '编辑通知渠道' : '添加通知渠道'} open={notifyModalOpen} onCancel={() => { setNotifyModalOpen(false); setEditingNotify(null); }} onOk={() => notifyForm.submit()} okText={editingNotify ? '保存' : '添加'}>
        <Form form={notifyForm} layout="vertical" onFinish={async (values: any) => {
          try {
            if (editingNotify) {
              await api.updateNotifyChannel(editingNotify, values);
              message.success('已更新');
            } else {
              await api.createNotifyChannel(values);
              message.success('已添加');
            }
            setNotifyModalOpen(false); setEditingNotify(null); notifyForm.resetFields(); fetchAll();
          } catch (e: any) { message.error(e.message); }
        }}>
          <Form.Item name="name" label="名称" rules={[{ required: !editingNotify }]}><Input disabled={!!editingNotify} placeholder="例如: 研发群" /></Form.Item>
          <Form.Item name="type" label="类型" initialValue="feishu">
            <Input disabled value="feishu" />
          </Form.Item>
          <Form.Item name="webhookUrl" label="Webhook URL" rules={[{ required: true, message: '请输入飞书机器人 Webhook 地址' }]}>
            <Input placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
