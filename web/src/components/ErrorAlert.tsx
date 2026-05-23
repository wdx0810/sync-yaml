import { Alert, Button } from 'antd';

interface ApiError {
  status?: number;
  message?: string;
  isNetworkError?: boolean;
}

interface Props {
  error: ApiError | null;
  onRetry?: () => void;
}

export default function ErrorAlert({ error, onRetry }: Props) {
  if (!error) return null;

  const description = error.isNetworkError
    ? '网络连接错误，请检查网络后重试'
    : `${error.status ? `HTTP ${error.status}: ` : ''}${error.message || '未知错误'}`;

  return (
    <Alert
      type="error"
      showIcon
      message="操作失败"
      description={description}
      action={
        onRetry ? (
          <Button size="small" onClick={onRetry}>
            重试
          </Button>
        ) : undefined
      }
      style={{ marginBottom: 16 }}
    />
  );
}
