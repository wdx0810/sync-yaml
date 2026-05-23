import { Checkbox, Divider, Space, Tag } from 'antd';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';

const resourceGroups = [
  {
    label: '核心资源',
    types: ['ConfigMap', 'Secret', 'Service', 'ServiceAccount'],
  },
  {
    label: '工作负载',
    types: ['Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job'],
  },
  {
    label: '网络',
    types: ['Ingress', 'NetworkPolicy'],
  },
  {
    label: '存储',
    types: ['PersistentVolumeClaim'],
  },
  {
    label: 'RBAC',
    types: ['Role', 'RoleBinding', 'ClusterRole', 'ClusterRoleBinding'],
  },
];

const allTypes = resourceGroups.flatMap(g => g.types);

interface Props {
  value?: string[];
  onChange?: (value: string[]) => void;
}

export default function ResourceTypeSelector({ value = [], onChange }: Props) {
  const isAll = value.length === 1 && value[0] === 'All';
  const selected = isAll ? [] : value;

  const handleAllChange = (e: CheckboxChangeEvent) => {
    if (e.target.checked) {
      onChange?.(['All']);
    } else {
      // Uncheck "all" — show individual checkboxes with nothing selected.
      onChange?.(['ConfigMap']); // default to at least one type
    }
  };

  const handleTypeChange = (type: string, checked: boolean) => {
    let newValue = selected.filter(v => v !== 'All');
    if (checked) {
      newValue = [...newValue, type];
    } else {
      newValue = newValue.filter(v => v !== type);
    }
    if (newValue.length === 0) {
      onChange?.(['All']);
    } else {
      onChange?.(newValue);
    }
  };

  return (
    <div>
      <Checkbox checked={isAll} onChange={handleAllChange} style={{ marginBottom: 8 }}>
        <strong>全部资源</strong>
      </Checkbox>
      {!isAll && (
        <div style={{ marginLeft: 24 }}>
          {resourceGroups.map(group => (
            <div key={group.label} style={{ marginBottom: 8 }}>
              <Tag color="blue">{group.label}</Tag>
              <Space wrap size={[4, 4]} style={{ marginTop: 4 }}>
                {group.types.map(type => (
                  <Checkbox
                    key={type}
                    checked={selected.includes(type)}
                    onChange={(e) => handleTypeChange(type, e.target.checked)}
                  >
                    {type}
                  </Checkbox>
                ))}
              </Space>
              <Divider style={{ margin: '4px 0' }} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export { allTypes, resourceGroups };
