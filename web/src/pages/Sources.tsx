import { useEffect, useState } from 'react';
import { Table, Button, Modal, Form, Input, InputNumber, Tag, Space, message, Popconfirm } from 'antd';
import { PlusOutlined, DeleteOutlined, CheckCircleOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { GitLabSource } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function Sources() {
  const [sources, setSources] = useState<GitLabSource[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [form] = Form.useForm();

  const fetchSources = async () => {
    setLoading(true); setError(null);
    try { const res = await api.getSources(); setSources(res.data || []); }
    catch (e) { setError(e); }
    finally { setLoading(false); }
  };

  useEffect(() => { fetchSources(); }, []);

  const handleCreate = async (values: any) => {
    try {
      await api.createSource(values);
      message.success('数据源已创建');
      setModalOpen(false); form.resetFields(); fetchSources();
    } catch (e: any) { message.error(e.message); }
  };

  const handleDelete = async (name: string) => {
    try { await api.deleteSource(name); message.success('已删除'); fetchSources(); }
    catch (e: any) { message.error(e.message); }
  };

  const handleTest = async (name: string) => {
    setTesting(name);
    try {
      const res = await api.testSource(name);
      if (res.data.success) message.success('连接成功');
      else message.error(`连接失败: ${res.data.error}`);
    } catch (e: any) { message.error(e.message); }
    finally { setTesting(null); }
  };

  const columns = [
    { title: '名称', dataIndex: 'name' },
    { title: 'URL', dataIndex: 'url' },
    { title: 'Project ID', dataIndex: 'projectId' },
    { title: '分支', dataIndex: 'branch' },
    { title: '状态', dataIndex: 'status', render: (s: string) => <Tag color={s === 'connected' ? 'green' : s === 'error' ? 'red' : 'default'}>{s}</Tag> },
    {
      title: '操作', render: (_: unknown, record: GitLabSource) => (
        <Space>
          <Button size="small" icon={<CheckCircleOutlined />} loading={testing === record.name} onClick={() => handleTest(record.name)}>测试</Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDelete(record.name)}>
            <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <h2>GitLab 连接管理</h2>
      <ErrorAlert error={error} onRetry={fetchSources} />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)} style={{ marginBottom: 16 }}>添加 GitLab 数据源</Button>
      <Table columns={columns} dataSource={sources} rowKey="name" loading={loading} />
      <Modal title="添加 GitLab 数据源" open={modalOpen} onCancel={() => setModalOpen(false)} onOk={() => form.submit()} okText="创建">
        <Form form={form} layout="vertical" onFinish={handleCreate}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="url" label="GitLab URL" rules={[{ required: true }]}><Input placeholder="https://gitlab.example.com" /></Form.Item>
          <Form.Item name="token" label="Access Token" rules={[{ required: true }]}><Input.Password /></Form.Item>
          <Form.Item name="projectId" label="Project ID" rules={[{ required: true }]}><InputNumber style={{ width: '100%' }} /></Form.Item>
          <Form.Item name="branch" label="分支" initialValue="main"><Input /></Form.Item>
          <Form.Item name="path" label="YAML 路径" initialValue="/"><Input /></Form.Item>
          <Form.Item name="webhookSecret" label="Webhook Secret"><Input.Password /></Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
