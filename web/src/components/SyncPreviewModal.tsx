import { useState } from 'react';
import { Modal, Tag, Space, Empty, Button, Collapse, Tooltip, Alert } from 'antd';
import { CheckOutlined, PlusOutlined, EditOutlined } from '@ant-design/icons';
import type { PendingChange, SyncSummary } from '../api/client';
import YamlDiffView from './YamlDiffView';

interface Props {
  open: boolean;
  onCancel: () => void;
  onApprove: (selected: PendingChange[]) => Promise<void>;
  changes: PendingChange[];
  summary?: SyncSummary;
  loading?: boolean;
  applying?: boolean;
}

export default function SyncPreviewModal({
  open, onCancel, onApprove, changes, summary, loading, applying,
}: Props) {
  // Selection state: all changes selected by default.
  const [unchecked, setUnchecked] = useState<Set<string>>(new Set());

  const keyOf = (c: PendingChange) => `${c.kind}|${c.namespace}|${c.name}`;

  const toggle = (c: PendingChange) => {
    const k = keyOf(c);
    setUnchecked(prev => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k); else next.add(k);
      return next;
    });
  };

  const selected = changes.filter(c => !unchecked.has(keyOf(c)));

  const handleApprove = async () => {
    await onApprove(selected);
    setUnchecked(new Set());
  };

  const handleCancel = () => {
    setUnchecked(new Set());
    onCancel();
  };

  return (
    <Modal
      title="同步预览 - 请审核即将应用到 K8s 的变更"
      open={open}
      onCancel={handleCancel}
      width={1100}
      style={{ top: 20 }}
      footer={[
        <Button key="cancel" onClick={handleCancel}>取消</Button>,
        <Button
          key="approve"
          type="primary"
          icon={<CheckOutlined />}
          loading={applying}
          disabled={selected.length === 0}
          onClick={handleApprove}
        >
          审核通过，应用 {selected.length} 项变更
        </Button>,
      ]}
    >
      {loading ? (
        <div style={{ padding: 40, textAlign: 'center' }}>正在计算变更...</div>
      ) : changes.length === 0 ? (
        <Empty description={
          <Space direction="vertical">
            <span>没有需要同步的变更</span>
            {summary && (
              <span style={{ color: '#94a3b8', fontSize: 12 }}>
                扫描 {summary.total} 项资源，跳过 {summary.skipped} 项
              </span>
            )}
          </Space>
        } />
      ) : (
        <>
          {summary && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message={
                <Space split={<span style={{ color: '#cbd5e1' }}>|</span>}>
                  <span>共 {summary.total} 项资源</span>
                  <span style={{ color: '#2563eb' }}>{changes.length} 项有变更</span>
                  <span style={{ color: '#64748b' }}>{summary.skipped} 项无变更</span>
                  <span style={{ color: '#0f172a' }}>已选中 <b>{selected.length}</b> 项</span>
                </Space>
              }
            />
          )}
          <Collapse
            accordion
            items={changes.map(c => {
              const k = keyOf(c);
              const isCreate = c.action === 'created';
              return {
                key: k,
                label: (
                  <Space>
                    <Tag color={isCreate ? 'green' : 'orange'} icon={isCreate ? <PlusOutlined /> : <EditOutlined />}>
                      {isCreate ? '新建' : '更新'}
                    </Tag>
                    <span><b>{c.kind}</b></span>
                    <span style={{ color: '#64748b' }}>{c.namespace || '-'} / {c.name}</span>
                  </Space>
                ),
                extra: (
                  <Tooltip title={unchecked.has(k) ? '已跳过' : '将应用'}>
                    <input
                      type="checkbox"
                      checked={!unchecked.has(k)}
                      onChange={() => toggle(c)}
                      onClick={(e) => e.stopPropagation()}
                      style={{ transform: 'scale(1.2)', cursor: 'pointer' }}
                    />
                  </Tooltip>
                ),
                children: (
                  <YamlDiffView
                    oldValue={c.oldYAML || '(不存在)'}
                    newValue={c.newYAML || ''}
                    leftTitle={isCreate ? '当前 (不存在)' : '当前集群中的资源'}
                    rightTitle="GitLab 期望的资源"
                  />
                ),
              };
            })}
          />
        </>
      )}
    </Modal>
  );
}
