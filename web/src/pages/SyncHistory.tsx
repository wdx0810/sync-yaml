import { useEffect, useState } from 'react';
import { Table, Input, Select, DatePicker, Space, Tag, Collapse } from 'antd';
import ReactDiffViewer from 'react-diff-viewer-continued';
import { api } from '../api/client';
import type { SyncRecord, HistoryFilter } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

const { RangePicker } = DatePicker;

export default function SyncHistory() {
  const [records, setRecords] = useState<SyncRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [filter, setFilter] = useState<HistoryFilter>({});
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);

  const fetchHistory = async () => {
    setLoading(true); setError(null);
    try {
      const res = await api.getHistory({ ...filter, page, pageSize });
      const data = res.data as any;
      if (data.records !== undefined) {
        // Paginated response
        setRecords(data.records || []);
        setTotal(data.total || 0);
      } else {
        // Backward compat: old non-paginated response
        setRecords(data || []);
        setTotal((data || []).length);
      }
    }
    catch (e) { setError(e); }
    finally { setLoading(false); }
  };

  useEffect(() => { fetchHistory(); }, [filter, page, pageSize]);

  const actionColor: Record<string, string> = { created: 'green', updated: 'blue', skipped: 'default', failed: 'red' };

  const columns = [
    { title: '时间', dataIndex: 'timestamp', render: (t: string) => new Date(t).toLocaleString(), width: 180 },
    { title: '任务', dataIndex: 'taskName', width: 150 },
    { title: '概要', dataIndex: 'configMapName', width: 150 },
    { title: '命名空间', dataIndex: 'namespace', width: 120 },
    {
      title: '方向', dataIndex: 'direction', width: 80,
      render: (d: string) => <Tag color={d === 'forward' ? 'blue' : 'purple'}>{d === 'forward' ? '正向' : '反向'}</Tag>,
    },
    {
      title: '状态', dataIndex: 'status', width: 80,
      render: (s: string) => <Tag color={s === 'Synced' ? 'green' : s === 'Failed' ? 'red' : 'orange'}>{s}</Tag>,
    },
  ];

  const [detailsCache, setDetailsCache] = useState<Record<string, any>>({});

  const loadDetails = async (id: string) => {
    if (detailsCache[id]) return;
    try {
      const res = await api.getHistoryRecord(id);
      setDetailsCache(prev => ({ ...prev, [id]: (res.data as any).details || [] }));
    } catch { /* ignore */ }
  };

  const expandedRowRender = (record: SyncRecord) => {
    const details = detailsCache[record.id];
    if (!details) {
      return <span style={{ color: '#999' }}>加载中...</span>;
    }
    if (details.length === 0) {
      return <span style={{ color: '#999' }}>无详细变更记录</span>;
    }

    const items = details
      .filter((d: any) => d.action !== 'skipped')
      .map((d: any, i: number) => ({
        key: String(i),
        label: (
          <Space>
            <strong>{d.namespace}/{d.name}</strong>
            <Tag color={actionColor[d.action] || 'default'}>{d.action}</Tag>
            {d.kind && <Tag>{d.kind}</Tag>}
            {d.error && <Tag color="red">{d.error}</Tag>}
          </Space>
        ),
        children: (
          <div style={{ maxHeight: 500, overflow: 'auto' }}>
            {(d.oldYaml || d.newYaml) ? (
              <ReactDiffViewer
                oldValue={d.oldYaml || ''}
                newValue={d.newYaml || ''}
                splitView={true}
                leftTitle={d.action === 'created' ? '(新建)' : '变更前'}
                rightTitle="变更后"
                useDarkTheme={false}
              />
            ) : d.changes?.length > 0 ? (
              <div>
                {d.changes.map((c: any, j: number) => (
                  <div key={j} style={{ marginBottom: 4 }}>
                    <Tag color={c.type === 'added' ? 'green' : c.type === 'deleted' ? 'red' : 'orange'}>{c.type}</Tag>
                    <code>{c.key}</code>
                    {c.type === 'modified' && <span>: <del style={{ color: 'red' }}>{c.oldValue}</del> → <span style={{ color: 'green' }}>{c.newValue}</span></span>}
                    {c.type === 'added' && <span>: <span style={{ color: 'green' }}>{c.newValue}</span></span>}
                    {c.type === 'deleted' && <span>: <del style={{ color: 'red' }}>{c.oldValue}</del></span>}
                  </div>
                ))}
              </div>
            ) : (
              <span style={{ color: '#999' }}>无内容变更</span>
            )}
          </div>
        ),
      }));

    const skippedCount = details.filter((d: any) => d.action === 'skipped').length;

    return (
      <div style={{ maxHeight: 600, overflow: 'auto' }}>
        {items.length > 0 && <Collapse items={items} />}
        {skippedCount > 0 && (
          <div style={{ marginTop: 8, color: '#999' }}>
            另有 {skippedCount} 个 ConfigMap 无变更已跳过
          </div>
        )}
        {items.length === 0 && skippedCount > 0 && (
          <span style={{ color: '#999' }}>所有 {skippedCount} 个 ConfigMap 均无变更</span>
        )}
      </div>
    );
  };

  return (
    <div>
      <h2>同步历史</h2>

      <ErrorAlert error={error} onRetry={fetchHistory} />
      <Space style={{ marginBottom: 16 }} wrap>
        <Input placeholder="ConfigMap 名称" allowClear onChange={(e) => setFilter((f) => ({ ...f, name: e.target.value || undefined }))} style={{ width: 160 }} />
        <Input placeholder="命名空间" allowClear onChange={(e) => setFilter((f) => ({ ...f, namespace: e.target.value || undefined }))} style={{ width: 160 }} />
        <Select placeholder="同步方向" allowClear onChange={(v) => setFilter((f) => ({ ...f, direction: v }))} style={{ width: 120 }}
          options={[{ label: '正向', value: 'forward' }, { label: '反向', value: 'reverse' }]} />
        <RangePicker onChange={(dates) => {
          if (dates && dates[0] && dates[1]) {
            setFilter((f) => ({ ...f, since: dates[0]!.toISOString(), until: dates[1]!.toISOString() }));
          } else {
            setFilter((f) => ({ ...f, since: undefined, until: undefined }));
          }
        }} />
      </Space>
      <Table
        columns={columns}
        dataSource={records}
        rowKey="id"
        loading={loading}
        pagination={{
          current: page,
          pageSize: pageSize,
          total: total,
          showSizeChanger: true,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (p, ps) => { setPage(p); setPageSize(ps); },
        }}
        scroll={{ y: 'calc(100vh - 300px)' }}
        expandable={{
          expandedRowRender,
          rowExpandable: () => true,
          onExpand: (expanded, record) => { if (expanded) loadDetails(record.id); },
        }}
      />
    </div>
  );
}
