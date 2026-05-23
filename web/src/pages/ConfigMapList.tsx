import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Table } from 'antd';
import { api } from '../api/client';
import type { ConfigMapStatus } from '../api/client';
import SyncStatusBadge from '../components/SyncStatusBadge';
import ErrorAlert from '../components/ErrorAlert';

export default function ConfigMapList() {
  const [data, setData] = useState<ConfigMapStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);

  const fetchData = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.getConfigMaps();
      setData(res.data || []);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    const timer = setInterval(fetchData, 10000);
    return () => clearInterval(timer);
  }, []);

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (name: string, record: ConfigMapStatus) => (
        <Link to={`/configmaps/${record.namespace}/${name}`}>{name}</Link>
      ),
    },
    { title: '命名空间', dataIndex: 'namespace' },
    {
      title: '同步状态',
      dataIndex: 'syncStatus',
      render: (status: string) => <SyncStatusBadge status={status} />,
    },
    {
      title: '最近同步时间',
      dataIndex: 'lastSyncTime',
      render: (t: string) => (t ? new Date(t).toLocaleString() : '-'),
    },
  ];

  return (
    <div>
      <h2>ConfigMap 列表</h2>
      <ErrorAlert error={error} onRetry={fetchData} />
      <Table
        columns={columns}
        dataSource={data}
        rowKey={(r) => `${r.namespace}/${r.name}`}
        loading={loading}
      />
    </div>
  );
}
