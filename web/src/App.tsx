import { useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route, Link, Navigate, useLocation } from 'react-router-dom';
import { Layout, Menu, Modal, Form, Input, message, Dropdown } from 'antd';
import {
  DashboardOutlined,
  DatabaseOutlined,
  SyncOutlined,
  HistoryOutlined,
  LogoutOutlined,
  KeyOutlined,
  SafetyOutlined,
  SettingOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import Dashboard from './pages/Dashboard';
import Connections from './pages/Connections';
import Tasks from './pages/Tasks';
import SyncHistory from './pages/SyncHistory';
import MFASettings from './pages/MFASettings';
import Users from './pages/Users';
import Login from './pages/Login';
import { api } from './api/client';
import './App.css';

const { Header, Content, Sider } = Layout;

function AppContent({ username, onLogout }: { username: string; onLogout: () => void }) {
  const location = useLocation();
  const [pwModalOpen, setPwModalOpen] = useState(false);
  const [pwForm] = Form.useForm();
  const [role, setRole] = useState<string>(localStorage.getItem('role') || '');

  useEffect(() => {
    // Fetch current user role if unknown.
    if (!role) {
      api.getCurrentUser()
        .then(res => {
          setRole(res.data.role);
          localStorage.setItem('role', res.data.role);
        })
        .catch(() => {});
    }
  }, [role]);

  const handleChangePassword = async (values: any) => {
    try {
      const axios = (await import('axios')).default;
      const token = localStorage.getItem('token');
      await axios.post('/api/v1/auth/change-password', values, {
        headers: { Authorization: `Bearer ${token}` },
      });
      message.success('密码已修改，请重新登录');
      setPwModalOpen(false);
      pwForm.resetFields();
      onLogout();
    } catch (e: any) {
      message.error(e.response?.data?.message || '修改失败');
    }
  };

  const selectedKey = location.pathname.split('/')[1] || 'dashboard';

  const userMenuItems = [
    { key: 'password', icon: <KeyOutlined />, label: '修改密码', onClick: () => setPwModalOpen(true) },
    { key: 'mfa', icon: <SafetyOutlined />, label: 'MFA 设置', onClick: () => window.location.hash = '' },
    { type: 'divider' as const },
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true, onClick: onLogout },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider width={220} theme="dark">
        <div className="app-logo">
          <div className="app-logo-icon">Y</div>
          <span className="app-logo-text">YAML Sync</span>
        </div>
        <div className="menu-section-label">主菜单</div>
        <Menu
          mode="inline"
          theme="dark"
          selectedKeys={[selectedKey]}
          items={[
            { key: 'dashboard', icon: <DashboardOutlined />, label: <Link to="/dashboard">仪表盘</Link> },
            { key: 'connections', icon: <DatabaseOutlined />, label: <Link to="/connections">数据源</Link> },
            { key: 'tasks', icon: <SyncOutlined />, label: <Link to="/tasks">同步任务</Link> },
            { key: 'history', icon: <HistoryOutlined />, label: <Link to="/history">同步历史</Link> },
          ]}
        />
        <div className="menu-section-label">设置</div>
        <Menu
          mode="inline"
          theme="dark"
          selectedKeys={[selectedKey]}
          items={[
            ...(role === 'admin' ? [{ key: 'users', icon: <TeamOutlined />, label: <Link to="/users">用户与权限</Link> }] : []),
            { key: 'mfa', icon: <SafetyOutlined />, label: <Link to="/mfa">安全认证</Link> },
          ]}
        />
      </Sider>
      <Layout>
        <Header className="app-header">
          <Dropdown menu={{ items: userMenuItems }} placement="bottomRight" trigger={['click']}>
            <div className="user-info" style={{ cursor: 'pointer' }}>
              <div className="user-avatar">{username[0]?.toUpperCase()}</div>
              <span className="user-name">{username}</span>
              {role === 'admin' && <span style={{ color: '#f59e0b', fontSize: 11, fontWeight: 600, marginLeft: 4 }}>ADMIN</span>}
              <SettingOutlined style={{ color: '#94a3b8', fontSize: 12 }} />
            </div>
          </Dropdown>
        </Header>
        <Content style={{ padding: 28, margin: 0, minHeight: 280, overflow: 'auto' }}>
          <Routes>
            <Route path="/" element={<Navigate to="/dashboard" replace />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/connections" element={<Connections />} />
            <Route path="/tasks" element={<Tasks />} />
            <Route path="/history" element={<SyncHistory />} />
            <Route path="/mfa" element={<MFASettings />} />
            {role === 'admin' && <Route path="/users" element={<Users />} />}
          </Routes>
        </Content>
      </Layout>

      <Modal title="修改密码" open={pwModalOpen} onCancel={() => setPwModalOpen(false)} onOk={() => pwForm.submit()} okText="确认修改">
        <Form form={pwForm} layout="vertical" onFinish={handleChangePassword}>
          <Form.Item name="oldPassword" label="旧密码" rules={[{ required: true }]}><Input.Password /></Form.Item>
          <Form.Item name="newPassword" label="新密码" rules={[{ required: true, min: 6, message: '至少 6 位' }]}><Input.Password /></Form.Item>
        </Form>
      </Modal>
    </Layout>
  );
}

function App() {
  const [token, setToken] = useState<string | null>(localStorage.getItem('token'));
  const [username, setUsername] = useState<string>(localStorage.getItem('username') || '');

  const handleLogin = (t: string, u: string) => {
    setToken(t);
    setUsername(u);
  };

  const handleLogout = () => {
    localStorage.removeItem('token');
    localStorage.removeItem('username');
    localStorage.removeItem('role');
    setToken(null);
    setUsername('');
  };

  if (!token) {
    return <Login onLogin={handleLogin} />;
  }

  return (
    <BrowserRouter>
      <AppContent username={username} onLogout={handleLogout} />
    </BrowserRouter>
  );
}

export default App;
