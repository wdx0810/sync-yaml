import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { Button, Card, Spin, message } from 'antd';
import { SyncOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { ConfigMapDetail as DetailType } from '../api/client';
import YamlDiffView from '../components/YamlDiffView';
import YamlHighlight from '../components/YamlHighlight';
import SyncStatusBadge from '../components/SyncStatusBadge';
import ErrorAlert from '../components/ErrorAlert';
import yaml from '../utils/yaml';

export default function ConfigMapDetail() {
  const { namespace, name } = useParams<{ namespace: string; name: string }>();
  const [detail, setDetail] = useState<DetailType | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [syncing, setSyncing] = useState(false);

  const fetchDetail = async () => {
    if (!namespace || !name) return;
    setLoading(true);
    setError(null);
    try {
      const res = await api.getConfigMapDetail(namespace, name);
      setDetail(res.data);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDetail();
  }, [namespace, name]);

  const handleSync = async () => {
    if (!namespace || !name) return;
    setSyncing(true);
    try {
      await api.forwardSyncOne(namespace, name);
      message.success('同步成功');
      fetchDetail();
    } catch (e: any) {
      message.error(e.message || '同步失败');
    } finally {
      setSyncing(false);
    }
  };

  if (loading) return <Spin size="large" />;

  return (
    <div>
      <h2>
        {namespace}/{name}{' '}
        {detail && <SyncStatusBadge status={detail.syncStatus} />}
      </h2>
      <ErrorAlert error={error} onRetry={fetchDetail} />
      <Button
        type="primary"
        icon={<SyncOutlined />}
        loading={syncing}
        onClick={handleSync}
        style={{ marginBottom: 16 }}
      >
        同步此 ConfigMap
      </Button>
      {detail && (
        <>
          {detail.diff && detail.diff.length > 0 ? (
            <Card title="YAML 差异对比" style={{ marginBottom: 16 }}>
              <YamlDiffView
                oldValue={yaml.stringify(detail.desiredState)}
                newValue={yaml.stringify(detail.actualState)}
              />
            </Card>
          ) : (
            <Card title="YAML 内容" style={{ marginBottom: 16 }}>
              <YamlHighlight code={yaml.stringify(detail.desiredState)} />
            </Card>
          )}
        </>
      )}
    </div>
  );
}
