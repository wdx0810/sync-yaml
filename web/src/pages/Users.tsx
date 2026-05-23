import { useEffect, useMemo, useState } from 'react';
import {
  Table, Button, Modal, Form, Input, Select, Tag, Space, message, Popconfirm,
  Switch, Drawer, Checkbox, Card, Empty, Tabs, Tooltip,
} from 'antd';
import {
  PlusOutlined, DeleteOutlined, EditOutlined, KeyOutlined, SafetyOutlined,
  FolderOutlined, UnorderedListOutlined,
} from '@ant-design/icons';
import { api } from '../api/client';
import type { User, SyncTask, TaskPermission, ProjectPermission, UserPermissions } from '../api/client';
import ErrorAlert from '../components/ErrorAlert';

export default function Users() {
  const [users, setUsers] = useState<User[]>([]);
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<any>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [form] = Form.useForm();

  const [permDrawerOpen, setPermDrawerOpen] = useState(false);
  const [permUser, setPermUser] = useState<User | null>(null);
  const [taskPerms, setTaskPerms] = useState<TaskPermission[]>([]);
  const [projectPerms, setProjectPerms] = useState<ProjectPermission[]>([]);
  const [permLoading, setPermLoading] = useState(false);

  const fetchAll = async () => {
    setLoading(true); setError(null);
    try {
      const [u, t] = await Promise.all([api.getUsers(), api.getTasks()]);
      setUsers(u.data || []);
      setTasks(t.data || []);
    } catch (e) { setError(e); }
    finally { setLoading(false); }
  };
  useEffect(() => { fetchAll(); }, []);

  const openCreate = () => {
    setEditingUser(null);
    form.resetFields();
    form.setFieldsValue({ role: 'user', enabled: true });
    setModalOpen(true);
  };

  const openEdit = (user: User) => {
    setEditingUser(user);
    form.setFieldsValue({ ...user, password: '' });
    setModalOpen(true);
  };

  const handleSubmit = async (values: any) => {
    try {
      if (editingUser) {
        const payload: any = { role: values.role, enabled: values.enabled };
        if (values.password) payload.password = values.password;
        await api.updateUser(editingUser.username, payload);
        message.success('用户已更新');
      } else {
        await api.createUser(values);
        message.success('用户已创建');
      }
      setModalOpen(false);
      form.resetFields();
      fetchAll();
    } catch (e: any) {
      message.error(e.message || '操作失败');
    }
  };

  const handleDelete = async (username: string) => {
    try {
      await api.deleteUser(username);
      message.success('用户已删除');
      fetchAll();
    } catch (e: any) {
      message.error(e.message || '删除失败');
    }
  };

  const handleResetMFA = async (username: string) => {
    try {
      await api.resetUserMFA(username);
      message.success('已清除该用户的 MFA 密钥');
      fetchAll();
    } catch (e: any) {
      message.error(e.message || '重置失败');
    }
  };

  const handleToggleMFA = async (username: string, enabled: boolean) => {
    try {
      await api.setUserMFAEnabled(username, enabled);
      message.success(enabled ? '已为该用户开启 MFA' : '已为该用户关闭 MFA');
      fetchAll();
    } catch (e: any) {
      message.error(e.message || '操作失败');
    }
  };

  const openPermissions = async (user: User) => {
    setPermUser(user);
    setPermDrawerOpen(true);
    setPermLoading(true);
    try {
      const res = await api.getUserPermissions(user.username);
      const data = res.data as UserPermissions;
      setTaskPerms(data.permissions || []);
      setProjectPerms(data.projectPermissions || []);
    } catch (e: any) {
      message.error(e.message || '加载权限失败');
      setTaskPerms([]);
      setProjectPerms([]);
    } finally {
      setPermLoading(false);
    }
  };

  // ---- Task permission helpers ----
  const getTaskPerm = (taskId: string): TaskPermission => {
    return taskPerms.find(p => p.taskId === taskId) ||
      { taskId, canView: false, canSync: false, canEdit: false };
  };

  const setTaskPerm = async (taskId: string, changes: Partial<TaskPermission>) => {
    if (!permUser) return;
    const current = getTaskPerm(taskId);
    const next = { ...current, ...changes };
    if ((next.canSync || next.canEdit) && !next.canView) next.canView = true;
    try {
      await api.setTaskPermission(permUser.username, taskId, {
        canView: next.canView, canSync: next.canSync, canEdit: next.canEdit,
      });
      setTaskPerms(prev => {
        const idx = prev.findIndex(p => p.taskId === taskId);
        if (idx >= 0) { const copy = [...prev]; copy[idx] = next; return copy; }
        return [...prev, next];
      });
    } catch (e: any) {
      message.error(e.message || '保存失败');
    }
  };

  const clearTaskPerm = async (taskId: string) => {
    if (!permUser) return;
    try {
      await api.removeTaskPermission(permUser.username, taskId);
      setTaskPerms(prev => prev.filter(p => p.taskId !== taskId));
    } catch (e: any) {
      message.error(e.message || '清除失败');
    }
  };

  // ---- Project permission helpers ----
  const projects = useMemo(
    () => Array.from(new Set(tasks.map(t => t.project).filter(Boolean) as string[])),
    [tasks]
  );

  const getProjectPerm = (project: string): ProjectPermission => {
    return projectPerms.find(p => p.project === project) ||
      { project, canView: false, canSync: false, canEdit: false };
  };

  const setProjectPerm = async (project: string, changes: Partial<ProjectPermission>) => {
    if (!permUser) return;
    const current = getProjectPerm(project);
    const next = { ...current, ...changes };
    if ((next.canSync || next.canEdit) && !next.canView) next.canView = true;
    try {
      await api.setProjectPermission(permUser.username, project, {
        canView: next.canView, canSync: next.canSync, canEdit: next.canEdit,
      });
      setProjectPerms(prev => {
        const idx = prev.findIndex(p => p.project === project);
        if (idx >= 0) { const copy = [...prev]; copy[idx] = next; return copy; }
        return [...prev, next];
      });
    } catch (e: any) {
      message.error(e.message || '保存失败');
    }
  };

  const clearProjectPerm = async (project: string) => {
    if (!permUser) return;
    try {
      await api.removeProjectPermission(permUser.username, project);
      setProjectPerms(prev => prev.filter(p => p.project !== project));
    } catch (e: any) {
      message.error(e.message || '清除失败');
    }
  };

  const columns = [
    {
      title: '用户名', dataIndex: 'username',
      render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span>,
    },
    {
      title: '角色', dataIndex: 'role', width: 100,
      render: (r: string) => (
        <Tag color={r === 'admin' ? 'gold' : 'blue'}>
          {r === 'admin' ? '管理员' : '普通用户'}
        </Tag>
      ),
    },
    {
      title: '状态', dataIndex: 'enabled', width: 80,
      render: (e: boolean) => <Tag color={e ? 'green' : 'default'}>{e ? '启用' : '禁用'}</Tag>,
    },
    {
      title: 'MFA', dataIndex: 'mfa', width: 160,
      render: (m: any, record: User) => {
        const configured = !!m?.configured;
        return (
          <Space>
            <Switch
              size="small"
              checked={!!m?.enabled}
              checkedChildren="开"
              unCheckedChildren="关"
              onChange={(checked) => {
                if (checked && !configured) {
                  message.warning('该用户尚未设置 MFA，需先由用户在安全认证页面扫码设置');
                  return;
                }
                handleToggleMFA(record.username, checked);
              }}
            />
            {m?.enabled ? (
              <Tag color="green" icon={<SafetyOutlined />} style={{ margin: 0 }}>已启用</Tag>
            ) : configured ? (
              <Tag style={{ margin: 0 }}>已关闭</Tag>
            ) : (
              <Tag style={{ margin: 0 }}>未设置</Tag>
            )}
          </Space>
        );
      },
    },
    {
      title: '操作', width: 360,
      render: (_: unknown, record: User) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(record)}>编辑</Button>
          {record.role !== 'admin' && (
            <Button size="small" icon={<SafetyOutlined />} onClick={() => openPermissions(record)}>
              权限
            </Button>
          )}
          {(record.mfa?.enabled || record.mfa?.configured) && (
            <Popconfirm
              title={`清除用户 ${record.username} 的 MFA 密钥？`}
              description="清除后该用户需重新扫码设置"
              onConfirm={() => handleResetMFA(record.username)}
            >
              <Tooltip title="用户丢失验证器时使用">
                <Button size="small">清除密钥</Button>
              </Tooltip>
            </Popconfirm>
          )}
          <Popconfirm title={`确认删除用户 ${record.username}？`} onConfirm={() => handleDelete(record.username)}>
            <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  // ---- Project permissions panel ----
  const projectPanel = projects.length === 0 ? (
    <Empty description="尚无项目分组，请先在同步任务中设置『项目分组』字段" />
  ) : (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      <div style={{ color: '#64748b', fontSize: 13 }}>
        项目级权限会应用到该项目下所有任务。若同一任务同时存在任务级和项目级权限，<b>任务级优先</b>。
      </div>
      {projects.map(project => {
        const perm = getProjectPerm(project);
        const hasAny = perm.canView || perm.canSync || perm.canEdit;
        const taskCount = tasks.filter(t => t.project === project).length;
        return (
          <Card
            key={project}
            size="small"
            title={
              <Space>
                <FolderOutlined style={{ color: '#2563eb' }} />
                <span><b>{project}</b></span>
                <Tag>{taskCount} 个任务</Tag>
              </Space>
            }
            extra={hasAny && (
              <Button size="small" type="link" danger onClick={() => clearProjectPerm(project)}>清除</Button>
            )}
          >
            <Space size={20}>
              <Checkbox checked={perm.canView} onChange={e => setProjectPerm(project, { canView: e.target.checked })}>查看</Checkbox>
              <Checkbox checked={perm.canSync} onChange={e => setProjectPerm(project, { canSync: e.target.checked })}>同步</Checkbox>
              <Checkbox checked={perm.canEdit} onChange={e => setProjectPerm(project, { canEdit: e.target.checked })}>编辑/删除</Checkbox>
            </Space>
          </Card>
        );
      })}
    </Space>
  );

  // ---- Task permissions panel ----
  const taskPanel = tasks.length === 0 ? (
    <Empty description="尚无同步任务" />
  ) : (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      <div style={{ color: '#64748b', fontSize: 13 }}>
        任务级权限用于对单个任务精细授权，<b>优先级高于项目级权限</b>。
      </div>
      {tasks.map(task => {
        const perm = getTaskPerm(task.id);
        const hasAny = perm.canView || perm.canSync || perm.canEdit;
        return (
          <Card
            key={task.id}
            size="small"
            title={
              <Space>
                <span>{task.name}</span>
                {task.project && <Tag>{task.project}</Tag>}
                <Tag color={task.direction === 'reverse' ? 'purple' : 'blue'}>
                  {task.direction === 'reverse' ? 'K8s → GitLab' : 'GitLab → K8s'}
                </Tag>
              </Space>
            }
            extra={hasAny && (
              <Button size="small" type="link" danger onClick={() => clearTaskPerm(task.id)}>清除</Button>
            )}
          >
            <Space size={20}>
              <Checkbox checked={perm.canView} onChange={e => setTaskPerm(task.id, { canView: e.target.checked })}>查看</Checkbox>
              <Checkbox checked={perm.canSync} onChange={e => setTaskPerm(task.id, { canSync: e.target.checked })}>同步</Checkbox>
              <Checkbox checked={perm.canEdit} onChange={e => setTaskPerm(task.id, { canEdit: e.target.checked })}>编辑/删除</Checkbox>
            </Space>
          </Card>
        );
      })}
    </Space>
  );

  return (
    <div>
      <h2>用户管理</h2>
      <ErrorAlert error={error} onRetry={fetchAll} />
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>创建用户</Button>
      </Space>
      <Table columns={columns} dataSource={users} rowKey="username" loading={loading} size="small" />

      <Modal
        title={editingUser ? `编辑用户 - ${editingUser.username}` : '创建用户'}
        open={modalOpen}
        onCancel={() => { setModalOpen(false); setEditingUser(null); }}
        onOk={() => form.submit()}
        okText={editingUser ? '保存' : '创建'}
        width={480}
      >
        <Form form={form} layout="vertical" onFinish={handleSubmit}>
          {!editingUser && (
            <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
              <Input prefix={<KeyOutlined />} placeholder="用户名" />
            </Form.Item>
          )}
          <Form.Item
            name="password"
            label={editingUser ? '新密码（留空不修改）' : '密码'}
            rules={editingUser ? [{ min: 6, message: '至少 6 位' }] : [{ required: true, min: 6, message: '至少 6 位' }]}
          >
            <Input.Password placeholder="至少 6 位" />
          </Form.Item>
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select options={[
              { label: '普通用户', value: 'user' },
              { label: '管理员', value: 'admin' },
            ]} />
          </Form.Item>
          <Form.Item name="enabled" label="启用状态" valuePropName="checked">
            <Switch checkedChildren="启用" unCheckedChildren="禁用" />
          </Form.Item>
        </Form>
      </Modal>

      <Drawer
        title={permUser ? `权限设置 - ${permUser.username}` : '权限设置'}
        open={permDrawerOpen}
        onClose={() => setPermDrawerOpen(false)}
        width={600}
      >
        {permLoading ? (
          '加载中...'
        ) : (
          <Tabs
            defaultActiveKey="project"
            items={[
              {
                key: 'project',
                label: <span><FolderOutlined /> 按项目授权</span>,
                children: projectPanel,
              },
              {
                key: 'task',
                label: <span><UnorderedListOutlined /> 按任务授权</span>,
                children: taskPanel,
              },
            ]}
          />
        )}
      </Drawer>
    </div>
  );
}
