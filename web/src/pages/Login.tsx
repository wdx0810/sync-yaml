import { useState } from 'react';
import { Form, Input, Button, message } from 'antd';
import { UserOutlined, LockOutlined, SafetyOutlined } from '@ant-design/icons';
import axios from 'axios';

interface Props {
  onLogin: (token: string, username: string) => void;
}

export default function Login({ onLogin }: Props) {
  const [loading, setLoading] = useState(false);
  const [mfaStep, setMfaStep] = useState(false);
  const [mfaUsername, setMfaUsername] = useState('');

  const handleSubmit = async (values: { username: string; password: string }) => {
    setLoading(true);
    try {
      const res = await axios.post('/api/v1/auth/login', values);
      if (res.data.mfaRequired) {
        setMfaUsername(res.data.username);
        setMfaStep(true);
      } else {
        const { token, username, role } = res.data;
        localStorage.setItem('token', token);
        localStorage.setItem('username', username);
        if (role) localStorage.setItem('role', role);
        onLogin(token, username);
      }
    } catch (e: any) {
      message.error(e.response?.data?.message || '登录失败');
    } finally {
      setLoading(false);
    }
  };

  const handleMFAVerify = async (values: { code: string }) => {
    setLoading(true);
    try {
      const res = await axios.post('/api/v1/auth/mfa/verify', {
        username: mfaUsername,
        code: values.code,
      });
      const { token, username, role } = res.data;
      localStorage.setItem('token', token);
      localStorage.setItem('username', username);
      if (role) localStorage.setItem('role', role);
      onLogin(token, username);
    } catch (e: any) {
      message.error(e.response?.data?.message || '验证码错误');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{
      display: 'flex',
      justifyContent: 'center',
      alignItems: 'center',
      minHeight: '100vh',
      background: 'linear-gradient(135deg, #1a1f36 0%, #2d1b69 50%, #1a1f36 100%)',
      position: 'relative',
      overflow: 'hidden',
    }}>
      {/* Background decoration */}
      <div style={{
        position: 'absolute',
        top: '-20%',
        right: '-10%',
        width: '500px',
        height: '500px',
        borderRadius: '50%',
        background: 'radial-gradient(circle, rgba(102,126,234,0.15) 0%, transparent 70%)',
      }} />
      <div style={{
        position: 'absolute',
        bottom: '-20%',
        left: '-10%',
        width: '600px',
        height: '600px',
        borderRadius: '50%',
        background: 'radial-gradient(circle, rgba(118,75,162,0.15) 0%, transparent 70%)',
      }} />

      <div style={{
        width: 400,
        background: 'rgba(255, 255, 255, 0.95)',
        backdropFilter: 'blur(20px)',
        borderRadius: 20,
        padding: '48px 40px',
        boxShadow: '0 20px 60px rgba(0, 0, 0, 0.3)',
        position: 'relative',
        zIndex: 1,
      }}>
        {/* Logo */}
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div style={{
            width: 56,
            height: 56,
            background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
            borderRadius: 14,
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            marginBottom: 16,
            boxShadow: '0 8px 24px rgba(102, 126, 234, 0.4)',
          }}>
            <span style={{ color: '#fff', fontSize: 24, fontWeight: 700 }}>Y</span>
          </div>
          <h1 style={{ fontSize: 22, fontWeight: 700, color: '#1e293b', margin: 0 }}>YAML Sync</h1>
          <p style={{ color: '#64748b', fontSize: 14, marginTop: 4 }}>GitLab ↔ Kubernetes 资源同步平台</p>
        </div>

        {!mfaStep ? (
          <>
            <Form onFinish={handleSubmit} size="large" layout="vertical">
              <Form.Item name="username" rules={[{ required: true, message: '请输入用户名' }]}>
                <Input
                  prefix={<UserOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="用户名"
                  style={{ borderRadius: 10, height: 44 }}
                />
              </Form.Item>
              <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
                <Input.Password
                  prefix={<LockOutlined style={{ color: '#94a3b8' }} />}
                  placeholder="密码"
                  style={{ borderRadius: 10, height: 44 }}
                />
              </Form.Item>
              <Form.Item style={{ marginBottom: 12 }}>
                <Button
                  type="primary"
                  htmlType="submit"
                  loading={loading}
                  block
                  style={{ height: 44, borderRadius: 10, fontSize: 15, fontWeight: 600 }}
                >
                  登录
                </Button>
              </Form.Item>
            </Form>
            <div style={{ color: '#94a3b8', textAlign: 'center', fontSize: 12 }}>默认账号: admin / admin123</div>
          </>
        ) : (
          <>
            <div style={{ textAlign: 'center', marginBottom: 24 }}>
              <div style={{
                width: 48,
                height: 48,
                background: '#f0f9ff',
                borderRadius: 12,
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                marginBottom: 12,
              }}>
                <SafetyOutlined style={{ fontSize: 22, color: '#667eea' }} />
              </div>
              <p style={{ color: '#475569', fontSize: 14 }}>请输入身份验证器中的 6 位验证码</p>
            </div>
            <Form onFinish={handleMFAVerify} size="large" layout="vertical">
              <Form.Item name="code" rules={[{ required: true, message: '请输入验证码' }, { len: 6, message: '验证码为 6 位数字' }]}>
                <Input
                  placeholder="000000"
                  maxLength={6}
                  style={{ textAlign: 'center', fontSize: 28, letterSpacing: 12, height: 56, borderRadius: 10, fontWeight: 600 }}
                  autoFocus
                />
              </Form.Item>
              <Form.Item>
                <Button type="primary" htmlType="submit" loading={loading} block style={{ height: 44, borderRadius: 10, fontSize: 15, fontWeight: 600 }}>
                  验证
                </Button>
              </Form.Item>
              <Button type="link" block onClick={() => { setMfaStep(false); setMfaUsername(''); }} style={{ color: '#64748b' }}>
                ← 返回登录
              </Button>
            </Form>
          </>
        )}
      </div>
    </div>
  );
}
