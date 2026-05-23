import { useEffect, useState, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { Table, Button, Space, Tag, message, Empty, Tree } from 'antd';
import { PlayCircleOutlined, PauseCircleOutlined, SyncOutlined, PlusOutlined, FolderOutlined, FileOutlined, AppstoreOutlined, CheckCircleOutlined, ClockCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { api } from '../api/client';
import type { DashboardData, SyncTask } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function Dashboard() {
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);
  const [selectedProject, setSelectedProject] = useState<string | null>(null);
  const [siderWidth, setSiderWidth] = useState(220);
  const [dragging, setDragging] = useState(false);

  const fetchData = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.getDashboard();
      setData(res.data);
    } catch (e) { setError(e); }
    finally { setLoading(false); }
  };

  useEffect(() => {
    fetchData();
    const t = setInterval(fetchData, 5000);
    return () => clearInterval(t);
  }, []);

  const [syncingId, setSyncingId] = useState<string | null>(null);

  const handleStart = async (id: string) => {
    try { await api.startTask(id); message.success('任务已启动'); fetchData(); }
    catch (e: any) { message.error(e.message); }
  };
  const handlePause = async (id: string) => {
    try { await api.pauseTask(id); message.success('任务已暂停'); fetchData(); }
    catch (e: any) { message.error(e.message); }
  };
  const handleSync = async (id: string) => {
    setSyncingId(id);
    try {
      const res = await api.syncTask(id);
      const info = res.data as any;
      if (info.total !== undefined) {
        const parts = [`总计 ${info.total} 个`];
        if (info.synced > 0) parts.push(`${info.synced} 个已同步`);
        if (info.skipped > 0) parts.push(`${info.skipped} 个无变更跳过`);
        if (info.failed > 0) parts.push(`${info.failed} 个失败`);
        message.success(`同步完成: ${parts.join(', ')}`);
      } else {
        message.success('同步完成');
      }
      fetchData();
    } catch (e: any) { message.error(e.message); }
    finally { setSyncingId(null); }
  };

  // Resizable sider.
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setDragging(true);
    const startX = e.clientX;
    const startWidth = siderWidth;
    const onMouseMove = (ev: MouseEvent) => {
      setSiderWidth(Math.max(160, Math.min(400, startWidth + ev.clientX - startX)));
    };
    const onMouseUp = () => {
      setDragging(false);
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
    };
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
  }, [siderWidth]);

  // Tree data.
  const buildTreeData = () => {
    const tasks = data?.tasks || [];
    const projectMap = new Map<string, SyncTask[]>();
    const ungrouped: SyncTask[] = [];
    for (const task of tasks) {
      const proj = task.project || '';
      if (proj) {
        if (!projectMap.has(proj)) projectMap.set(proj, []);
        projectMap.get(proj)!.push(task);
      } else {
        ungrouped.push(task);
      }
    }
    const treeData: any[] = [{ key: '__all__', title: `全部 (${tasks.length})`, icon: <AppstoreOutlined /> }];
    for (const proj of [...projectMap.keys()].sort()) {
      const projTasks = projectMap.get(proj)!;
      treeData.push({
        key: `project:${proj}`,
        title: `${proj} (${projTasks.length})`,
        icon: <FolderOutlined />,
        children: projTasks.map(t => ({ key: `task:${t.id}`, title: t.name, icon: <FileOutlined />, isLeaf: true })),
      });
    }
    if (ungrouped.length > 0) {
      treeData.push({
        key: '__ungrouped__',
        title: `未分组 (${ungrouped.length})`,
        icon: <FolderOutlined />,
        children: ungrouped.map(t => ({ key: `task:${t.id}`, title: t.name, icon: <FileOutlined />, isLeaf: true })),
      });
    }
    return treeData;
  };

  const handleTreeSelect = (selectedKeys: any[]) => {
    if (selectedKeys.length === 0) { setSelectedProject(null); return; }
    const key = selectedKeys[0] as string;
    if (key === '__all__') setSelectedProject(null);
    else if (key.startsWith('project:')) setSelectedProject(key.replace('project:', ''));
    else if (key === '__ungrouped__') setSelectedProject('__ungrouped__');
    else if (key.startsWith('task:')) {
      const task = data?.tasks.find(t => t.id === key.replace('task:', ''));
      if (task) setSelectedProject(task.project || '__ungrouped__');
    }
  };

  const allTasks = data?.tasks || [];
  const filteredTasks = selectedProject === null ? allTasks : selectedProject === '__ungrouped__' ? allTasks.filter(t => !t.project) : allTasks.filter(t => t.project === selectedProject);

  const statusColor: Record<string, string> = { running: 'green', paused: 'orange', error: 'red' };

  const columns = [
    { title: '任务', dataIndex: 'name', render: (n: string) => <span style={{ fontWeight: 500 }}>{n}</span> },
    { title: '方向', dataIndex: 'direction', width: 110, render: (d: string) => <Tag color={d === 'forward' ? 'blue' : 'purple'}>{d === 'forward' ? 'GitLab→K8s' : 'K8s→GitLab'}</Tag> },
    { title: '模式', dataIndex: 'syncMode', width: 80, render: (m: string) => <Tag>{m}</Tag> },
    { title: '状态', dataIndex: 'status', width: 80, render: (s: string) => <Tag color={statusColor[s] || 'default'}>{s}</Tag> },
    { title: '最近同步', dataIndex: 'lastSyncTime', width: 160, render: (t: string) => t ? new Date(t).toLocaleString() : <span style={{ color: '#94a3b8' }}>-</span> },
    { title: '结果', dataIndex: 'lastSyncResult', ellipsis: true, render: (r: string) => r || <span style={{ color: '#94a3b8' }}>-</span> },
    {
      title: '操作', width: 160, render: (_: unknown, record: SyncTask) => (
        <Space>
          {record.status === 'paused' || record.status === 'error' ? (
            <Button size="small" icon={<PlayCircleOutlined />} onClick={() => handleStart(record.id)}>启动</Button>
          ) : (
            <Button size="small" icon={<PauseCircleOutlined />} onClick={() => handlePause(record.id)}>暂停</Button>
          )}
          <Button size="small" type="primary" icon={<SyncOutlined spin={syncingId === record.id} />} loading={syncingId === record.id} onClick={() => handleSync(record.id)}>
            同步
          </Button>
        </Space>
      ),
    },
  ];

  const statCards = data ? [
    { label: '总任务', value: data.summary.total, icon: <AppstoreOutlined />, color: '#667eea', bg: '#f0f4ff' },
    { label: '运行中', value: data.summary.running, icon: <CheckCircleOutlined />, color: '#10b981', bg: '#ecfdf5' },
    { label: '已暂停', value: data.summary.paused, icon: <ClockCircleOutlined />, color: '#f59e0b', bg: '#fffbeb' },
    { label: '错误', value: data.summary.error, icon: <CloseCircleOutlined />, color: '#ef4444', bg: '#fef2f2' },
  ] : [];

  return (
    <div>
      <div className="page-header">
        <h2>仪表盘</h2>
        <p>实时监控同步任务状态</p>
      </div>
      <ErrorAlert error={error} onRetry={fetchData} />

      {/* Stat cards */}
      {data && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginBottom: 24 }}>
          {statCards.map((s) => (
            <div key={s.label} style={{
              background: '#fff',
              borderRadius: 12,
              padding: '20px 24px',
              border: '1px solid #edf2f7',
              display: 'flex',
              alignItems: 'center',
              gap: 16,
              transition: 'transform 0.2s, box-shadow 0.2s',
            }}>
              <div style={{
                width: 48,
                height: 48,
                borderRadius: 12,
                background: s.bg,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 20,
                color: s.color,
              }}>
                {s.icon}
              </div>
              <div>
                <div style={{ fontSize: 28, fontWeight: 700, color: '#1e293b', lineHeight: 1 }}>{s.value}</div>
                <div style={{ fontSize: 13, color: '#64748b', marginTop: 4 }}>{s.label}</div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Task list with tree */}
      {data && data.tasks.length === 0 ? (
        <div style={{ background: '#fff', borderRadius: 12, padding: 60, textAlign: 'center', border: '1px solid #edf2f7' }}>
          <Empty description={<span style={{ color: '#64748b' }}>暂无同步任务</span>}>
            <Link to="/tasks"><Button type="primary" icon={<PlusOutlined />} size="large" style={{ marginTop: 8 }}>创建第一个同步任务</Button></Link>
          </Empty>
        </div>
      ) : (
        <div style={{ display: 'flex', background: '#fff', border: '1px solid #edf2f7', borderRadius: 12, overflow: 'hidden', minHeight: 360 }}>
          <div style={{ width: siderWidth, minWidth: 160, maxWidth: 400, borderRight: '1px solid #edf2f7', background: '#fafbfc', position: 'relative', flexShrink: 0, padding: '16px 0' }}>
            <div style={{ padding: '0 16px 12px', fontWeight: 600, fontSize: 13, color: '#475569' }}>项目导航</div>
            <Tree
              showIcon
              defaultExpandAll
              selectedKeys={selectedProject === null ? ['__all__'] : selectedProject === '__ungrouped__' ? ['__ungrouped__'] : [`project:${selectedProject}`]}
              onSelect={handleTreeSelect}
              treeData={buildTreeData()}
              style={{ padding: '0 8px' }}
            />
            <div
              onMouseDown={handleMouseDown}
              style={{ position: 'absolute', top: 0, right: -3, width: 6, height: '100%', cursor: 'col-resize', zIndex: 10, background: dragging ? 'rgba(102,126,234,0.15)' : 'transparent' }}
            />
          </div>
          <div style={{ flex: 1, padding: 16, overflow: 'auto', userSelect: dragging ? 'none' : 'auto' }}>
            <div style={{ marginBottom: 12, fontSize: 13, color: '#64748b' }}>
              {selectedProject === null ? '全部任务' : selectedProject === '__ungrouped__' ? '未分组' : selectedProject} · {filteredTasks.length} 个任务
            </div>
            <Table columns={columns} dataSource={filteredTasks} rowKey="id" loading={loading} size="small" pagination={false} scroll={{ x: 700 }} />
          </div>
        </div>
      )}
    </div>
  );
}
