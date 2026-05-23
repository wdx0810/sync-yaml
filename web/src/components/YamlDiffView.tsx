import ReactDiffViewer from 'react-diff-viewer-continued';

interface Props {
  oldValue: string;
  newValue: string;
  leftTitle?: string;
  rightTitle?: string;
}

export default function YamlDiffView({
  oldValue,
  newValue,
  leftTitle = 'GitLab (Desired)',
  rightTitle = 'K8s (Actual)',
}: Props) {
  return (
    <ReactDiffViewer
      oldValue={oldValue}
      newValue={newValue}
      splitView={true}
      leftTitle={leftTitle}
      rightTitle={rightTitle}
      useDarkTheme={false}
    />
  );
}
