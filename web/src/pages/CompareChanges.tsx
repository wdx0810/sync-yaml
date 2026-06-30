import { useEffect, useState } from 'react';
import { Select, DatePicker, Space, Tag, Collapse, Card, Button, message } from 'antd';
import { SearchOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { SyncTask } from '../api/client';
import { buildEnvOptions } from '../utils/taskEnv';

const { RangePicker } = DatePicker;

// Extract the resource type (parent folder name) from a GitLab file path.
// Paths look like ".../lion-app-eu/deployments/xxx.yaml" -> "deployments".
function resourceTypeOf(path: string): string {
  if (!path) return '未知';
  const parts = path.split('/').filter(Boolean);
  if (parts.length >= 2) return parts[parts.length - 2];
  return '未知';
}

// Build a text file from the compare diffs and trigger a browser download.
function exportDiffs(diffs: any[], taskId: string, range: [string, string] | null) {
  const lines: string[] = [];
  lines.push('YAML 变更对比导出');
  lines.push(`任务ID: ${taskId}`);
  if (range && range[0] && range[1]) {
    lines.push(`对比范围: ${range[0]}  ~  ${range[1]}`);
  }
  lines.push(`导出时间: ${new Date().toLocaleString()}`);
  lines.push(`文件变更数: ${diffs.length}`);
  lines.push('='.repeat(80));
  lines.push('');

  for (const d of diffs) {
    const status = d.newFile ? '[新增]' : d.deletedFile ? '[删除]' : '[修改]';
    lines.push(`${status} ${d.path}`);
    lines.push('-'.repeat(80));
    lines.push(d.diff || '(无内容差异)');
    lines.push('');
  }

  const content = lines.join('\n');
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  a.href = url;
  a.download = `yaml-diff-${taskId}-${ts}.txt`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function CompareSection() {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [taskId, setTaskId] = useState<string>('');
  const [mode, setMode] = useState<'time' | 'commit'>('time');
  const [dateRange, setDateRange] = useState<[string, string] | null>(null);
  const [commits, setCommits] = useState<any[]>([]);
  const [fromSHA, setFromSHA] = useState<string>('');
  const [toSHA, setToSHA] = useState<string>('');
  const [diffs, setDiffs] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [typeFilter, setTypeFilter] = useState<string[]>([]);

  useEffect(() => {
    api.getTasks().then(res => setTasks(res.data || [])).catch(() => {});
  }, []);

  // Load commits when task changes and mode is 'commit'.
  useEffect(() => {
    if (mode === 'commit' && taskId) {
      api.listCommits(taskId, 50).then(res => setCommits(res.data || [])).catch(() => {});
    }
  }, [mode, taskId]);

  const handleCompare = async () => {
    if (!taskId) { message.warning('请选择任务'); return; }
    setLoading(true);
    const hide = message.loading('正在从 GitLab 获取变更对比...', 0);
    try {
      let res: any;
      if (mode === 'time') {
        if (!dateRange) { message.warning('请选择时间范围'); hide(); setLoading(false); return; }
        res = await api.compareChanges(taskId, dateRange[0], dateRange[1]);
      } else {
        if (!fromSHA || !toSHA) { message.warning('请选择两个 commit'); hide(); setLoading(false); return; }
        res = await api.compareByCommit(taskId, fromSHA, toSHA);
      }
      hide();
      setDiffs(res.data.diffs || []);
      setTotal(res.data.total || 0);
      setTypeFilter([]);
      if (res.data.total === 0) message.info('该范围内无变更');
    } catch (e: any) {
      hide();
      message.error(e.message || '对比失败');
    } finally {
      setLoading(false);
    }
  };

  const availableTypes = Array.from(new Set(diffs.map((d: any) => resourceTypeOf(d.path)))).sort();
  const typeCounts: Record<string, number> = {};
  for (const d of diffs) {
    const t = resourceTypeOf(d.path);
    typeCounts[t] = (typeCounts[t] || 0) + 1;
  }
  const filteredDiffs = typeFilter.length === 0
    ? diffs
    : diffs.filter((d: any) => typeFilter.includes(resourceTypeOf(d.path)));

  return (
    <div>
      <Space wrap style={{ marginBottom: diffs.length > 0 ? 12 : 0 }}>
        <Select
          placeholder="选择环境(GitLab 路径)"
          value={taskId || undefined}
          onChange={(v) => { setTaskId(v); setDiffs([]); setTotal(0); setTypeFilter([]); }}
          style={{ width: 280 }}
          showSearch
          optionFilterProp="label"
          options={buildEnvOptions(tasks)}
        />
        <Select
          value={mode}
          onChange={(v) => { setMode(v); setDiffs([]); setTotal(0); setTypeFilter([]); }}
          style={{ width: 130 }}
          options={[{ label: '按时间段', value: 'time' }, { label: '按 Commit', value: 'commit' }]}
        />
        {mode === 'time' && (
          <RangePicker
            showTime
            onChange={(dates) => {
              if (dates && dates[0] && dates[1]) {
                setDateRange([dates[0].toISOString(), dates[1].toISOString()]);
              } else {
                setDateRange(null);
              }
            }}
          />
        )}
        {mode === 'commit' && (
          <>
            <Select
              placeholder="From (旧)"
              value={fromSHA || undefined}
              onChange={setFromSHA}
              style={{ width: 280 }}
              showSearch
              optionFilterProp="label"
              options={commits.map(c => ({ label: `${c.shortId} - ${c.title} (${new Date(c.createdAt).toLocaleDateString()})`, value: c.id }))}
            />
            <Select
              placeholder="To (新)"
              value={toSHA || undefined}
              onChange={setToSHA}
              style={{ width: 280 }}
              showSearch
              optionFilterProp="label"
              options={commits.map(c => ({ label: `${c.shortId} - ${c.title} (${new Date(c.createdAt).toLocaleDateString()})`, value: c.id }))}
            />
          </>
        )}
        <Button type="primary" icon={<SearchOutlined />} loading={loading} onClick={handleCompare}>
          对比变更
        </Button>
        {diffs.length > 0 && availableTypes.length > 0 && (
          <Select
            mode="multiple"
            allowClear
            placeholder="按资源类型筛选"
            value={typeFilter}
            onChange={setTypeFilter}
            style={{ minWidth: 220 }}
            maxTagCount="responsive"
            options={availableTypes.map(t => ({ label: `${t} (${typeCounts[t]})`, value: t }))}
          />
        )}
        {diffs.length > 0 && <Tag color="blue">{filteredDiffs.length} / {total} 个文件变更</Tag>}
        {filteredDiffs.length > 0 && (
          <Button onClick={() => exportDiffs(filteredDiffs, taskId, mode === 'time' ? dateRange : [fromSHA, toSHA])}>
            导出
          </Button>
        )}
      </Space>
      {filteredDiffs.length > 0 && (
        <Collapse
          items={filteredDiffs.map((d: any, i: number) => ({
            key: String(i),
            label: (
              <Space>
                <Tag color={d.newFile ? 'green' : d.deletedFile ? 'red' : 'orange'}>
                  {d.newFile ? '新增' : d.deletedFile ? '删除' : '修改'}
                </Tag>
                <Tag>{resourceTypeOf(d.path)}</Tag>
                <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{d.path}</span>
              </Space>
            ),
            children: (
              <pre style={{ maxHeight: 400, overflow: 'auto', background: '#f8fafc', padding: 12, fontSize: 12, borderRadius: 6 }}>
                {d.diff || '(无内容差异)'}
              </pre>
            ),
          }))}
        />
      )}
    </div>
  );
}

export default function CompareChanges() {
  return (
    <div>
      <h2>变更对比</h2>
      <Card size="small" title="📊 对比指定时间段或两个 Commit 之间的 GitLab YAML 变化" style={{ marginBottom: 16 }}>
        <CompareSection />
      </Card>
    </div>
  );
}
