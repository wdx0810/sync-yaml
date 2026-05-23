import { useEffect, useState } from 'react';
import { Table, Button, Space, message, Tag } from 'antd';
import { api } from '../api/client';
import type { DriftAlert } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function DriftAlerts() {
  const [alerts, setAlerts] = useState<DriftAlert[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);

  const fetchAlerts = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.getDriftAlerts();
      setAlerts(res.data || []);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchAlerts();
  }, []);

  const handleReverse = async (ns: string, name: string) => {
    try {
      await api.reverseSync(ns, name);
      message.success('反向同步成功');
      fetchAlerts();
    } catch (e: any) {
      message.error(e.message || '反向同步失败');
    }
  };

  const handleDismiss = async (id: string) => {
    try {
      await api.dismissAlert(id);
      message.success('已忽略');
      fetchAlerts();
    } catch (e: any) {
      message.error(e.message || '操作失败');
    }
  };

  const columns = [
    { title: 'ConfigMap', dataIndex: 'configMapName' },
    { title: '命名空间', dataIndex: 'namespace' },
    {
      title: '差异字段',
      dataIndex: 'diffFields',
      render: (fields: string[]) =>
        fields?.map((f) => <Tag key={f}>{f}</Tag>),
    },
    {
      title: '检测时间',
      dataIndex: 'detectedAt',
      render: (t: string) => new Date(t).toLocaleString(),
    },
    {
      title: '操作',
      render: (_: unknown, record: DriftAlert) => (
        <Space>
          <Button
            type="primary"
            size="small"
            onClick={() => handleReverse(record.namespace, record.configMapName)}
          >
            反向同步
          </Button>
          <Button size="small" onClick={() => handleDismiss(record.id)}>
            忽略
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <h2>漂移告警</h2>
      <ErrorAlert error={error} onRetry={fetchAlerts} />
      <Table
        columns={columns}
        dataSource={alerts}
        rowKey="id"
        loading={loading}
      />
    </div>
  );
}
