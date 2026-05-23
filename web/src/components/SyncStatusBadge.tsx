import { Tag } from 'antd';

const statusColors: Record<string, string> = {
  Synced: 'green',
  Pending: 'orange',
  Failed: 'red',
  Drifted: 'volcano',
};

interface Props {
  status: string;
}

export default function SyncStatusBadge({ status }: Props) {
  return <Tag color={statusColors[status] || 'default'}>{status}</Tag>;
}
