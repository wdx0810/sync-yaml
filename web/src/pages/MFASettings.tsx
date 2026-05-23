import { useEffect, useState } from 'react';
import { Card, Button, Input, Form, message, Steps, Alert, Space, Tag } from 'antd';
import { SafetyOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { api } from '../api/client';

export default function MFASettings() {
  const [enabled, setEnabled] = useState(false);
  const [loading, setLoading] = useState(false);
  const [setupData, setSetupData] = useState<{ secret: string; otpauthURL: string } | null>(null);
  const [step, setStep] = useState(0); // 0: idle, 1: show QR, 2: verify
  const [disableMode, setDisableMode] = useState(false);

  const fetchStatus = async () => {
    try {
      const res = await api.getMFAStatus();
      setEnabled(res.data.enabled);
    } catch { /* ignore */ }
  };

  useEffect(() => { fetchStatus(); }, []);

  const handleSetup = async () => {
    setLoading(true);
    try {
      const res = await api.setupMFA();
      setSetupData(res.data);
      setStep(1);
    } catch (e: any) {
      message.error(e.message || '设置失败');
    } finally {
      setLoading(false);
    }
  };

  const handleEnable = async (values: { code: string }) => {
    setLoading(true);
    try {
      await api.enableMFA(values.code);
      message.success('MFA 已启用');
      setEnabled(true);
      setStep(0);
      setSetupData(null);
    } catch (e: any) {
      message.error(e.message || '验证码错误');
    } finally {
      setLoading(false);
    }
  };

  const handleDisable = async (values: { code: string }) => {
    setLoading(true);
    try {
      await api.disableMFA(values.code);
      message.success('MFA 已禁用');
      setEnabled(false);
      setDisableMode(false);
    } catch (e: any) {
      message.error(e.message || '验证码错误');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <h2>MFA 二次认证</h2>
      <Card style={{ maxWidth: 600 }}>
        <Space direction="vertical" style={{ width: '100%' }} size="large">
          <div>
            <span style={{ marginRight: 12 }}>当前状态：</span>
            {enabled ? (
              <Tag icon={<CheckCircleOutlined />} color="success">已启用</Tag>
            ) : (
              <Tag icon={<CloseCircleOutlined />} color="default">未启用</Tag>
            )}
          </div>

          {!enabled && step === 0 && (
            <div>
              <Alert
                message="启用 MFA 后，每次登录需要输入身份验证器（如 Google Authenticator）中的 6 位验证码。"
                type="info"
                showIcon
                style={{ marginBottom: 16 }}
              />
              <Button type="primary" icon={<SafetyOutlined />} onClick={handleSetup} loading={loading}>
                启用 MFA
              </Button>
            </div>
          )}

          {!enabled && step === 1 && setupData && (
            <div>
              <Steps current={1} size="small" style={{ marginBottom: 24 }} items={[
                { title: '生成密钥' },
                { title: '扫码绑定' },
                { title: '验证启用' },
              ]} />
              <Alert
                message="请使用身份验证器扫描下方二维码，或手动输入密钥"
                type="info"
                showIcon
                style={{ marginBottom: 16 }}
              />
              <div style={{ textAlign: 'center', marginBottom: 16 }}>
                <img
                  src={`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(setupData.otpauthURL)}`}
                  alt="MFA QR Code"
                  style={{ border: '1px solid #d9d9d9', borderRadius: 8, padding: 8 }}
                />
              </div>
              <div style={{ textAlign: 'center', marginBottom: 16 }}>
                <span style={{ color: '#666' }}>手动输入密钥：</span>
                <code style={{ background: '#f5f5f5', padding: '4px 8px', borderRadius: 4, fontSize: 14, wordBreak: 'break-all' }}>
                  {setupData.secret}
                </code>
              </div>
              <Button type="primary" onClick={() => setStep(2)}>下一步：输入验证码</Button>
              <Button style={{ marginLeft: 8 }} onClick={() => { setStep(0); setSetupData(null); }}>取消</Button>
            </div>
          )}

          {!enabled && step === 2 && (
            <div>
              <Steps current={2} size="small" style={{ marginBottom: 24 }} items={[
                { title: '生成密钥' },
                { title: '扫码绑定' },
                { title: '验证启用' },
              ]} />
              <Alert
                message="请输入身份验证器中显示的 6 位验证码以完成启用"
                type="info"
                showIcon
                style={{ marginBottom: 16 }}
              />
              <Form onFinish={handleEnable} layout="inline">
                <Form.Item name="code" rules={[{ required: true, message: '请输入验证码' }, { len: 6, message: '6 位数字' }]}>
                  <Input placeholder="000000" maxLength={6} style={{ width: 140, textAlign: 'center', fontSize: 18, letterSpacing: 6 }} />
                </Form.Item>
                <Form.Item>
                  <Button type="primary" htmlType="submit" loading={loading}>确认启用</Button>
                </Form.Item>
              </Form>
            </div>
          )}

          {enabled && !disableMode && (
            <div>
              <Alert
                message="MFA 已启用，每次登录需要输入验证码。"
                type="success"
                showIcon
                style={{ marginBottom: 16 }}
              />
              <Button danger onClick={() => setDisableMode(true)}>禁用 MFA</Button>
            </div>
          )}

          {enabled && disableMode && (
            <div>
              <Alert
                message="请输入当前验证码以禁用 MFA"
                type="warning"
                showIcon
                style={{ marginBottom: 16 }}
              />
              <Form onFinish={handleDisable} layout="inline">
                <Form.Item name="code" rules={[{ required: true, message: '请输入验证码' }, { len: 6, message: '6 位数字' }]}>
                  <Input placeholder="000000" maxLength={6} style={{ width: 140, textAlign: 'center', fontSize: 18, letterSpacing: 6 }} />
                </Form.Item>
                <Form.Item>
                  <Button danger htmlType="submit" loading={loading}>确认禁用</Button>
                </Form.Item>
                <Form.Item>
                  <Button onClick={() => setDisableMode(false)}>取消</Button>
                </Form.Item>
              </Form>
            </div>
          )}
        </Space>
      </Card>
    </div>
  );
}
