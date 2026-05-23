import { useEffect, useState } from 'react';
import { Table, Button, Modal, Form, Input, Tag, Space, message, Popconfirm } from 'antd';
import { PlusOutlined, DeleteOutlined, CheckCircleOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { K8sTarget } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function Targets() {
  const [targets, setTargets] = useState<K8sTarget[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [form] = Form.useForm();

  const fetchTargets = async () => {
    setLoading(true); setError(null);
    try { const res = await api.getTargets(); setTargets(res.data || []); }
    catch (e) { setError(e); }
    finally { setLoading(false); }
  };

  useEffect(() => { fetchTargets(); }, []);

  const handleCreate = async (values: any) => {
    try {
      await api.createTarget(values);
      message.success('集群目标已创建');
      setModalOpen(false); form.resetFields(); fetchTargets();
    } catch (e: any) { message.error(e.message); }
  };

  const handleDelete = async (name: string) => {
    try { await api.deleteTarget(name); message.success('已删除'); fetchTargets(); }
    catch (e: any) { message.error(e.message); }
  };

  const handleTest = async (name: string) => {
    setTesting(name);
    try {
      const res = await api.testTarget(name);
      if (res.data.success) message.success('连接成功');
      else message.error(`连接失败: ${res.data.error}`);
    } catch (e: any) { message.error(e.message); }
    finally { setTesting(null); }
  };

  const columns = [
    { title: '名称', dataIndex: 'name' },
    { title: '命名空间', dataIndex: 'namespace' },
    { title: 'Kubeconfig', dataIndex: 'kubeconfigContent', render: (v: string) => v || '(未配置)' },
    { title: '状态', dataIndex: 'status', render: (s: string) => <Tag color={s === 'connected' ? 'green' : s === 'error' ? 'red' : 'default'}>{s}</Tag> },
    {
      title: '操作', render: (_: unknown, record: K8sTarget) => (
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
      <h2>K8s 集群管理</h2>
      <ErrorAlert error={error} onRetry={fetchTargets} />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)} style={{ marginBottom: 16 }}>添加 K8s 集群</Button>
      <Table columns={columns} dataSource={targets} rowKey="name" loading={loading} />
      <Modal title="添加 K8s 集群" open={modalOpen} onCancel={() => setModalOpen(false)} onOk={() => form.submit()} okText="创建">
        <Form form={form} layout="vertical" onFinish={handleCreate}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="kubeconfigContent" label="Kubeconfig 内容"><Input.TextArea rows={8} placeholder="粘贴 kubeconfig YAML 内容" /></Form.Item>
          <Form.Item name="namespace" label="命名空间" initialValue="default"><Input /></Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
