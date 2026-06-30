import type { SyncTask } from '../api/client';

// For the GitLab-only features (变更对比 / 配置变更), the sync direction is
// irrelevant — what matters is which GitLab repo + path the task points at.
// These helpers collapse the task list into one option per unique GitLab path
// so the user picks an "environment" (path) rather than a directional task.

// gitlabSourceOf returns the name of the GitLab data source a task uses.
// Reverse tasks have GitLab as the target; forward tasks have it as the source.
export function gitlabSourceOf(t: SyncTask): string {
  return t.direction === 'reverse' ? t.targetName : t.sourceName;
}

// envKey uniquely identifies a GitLab repo + path across tasks.
export function envKey(t: SyncTask): string {
  return `${gitlabSourceOf(t)}|${t.sourcePath || ''}`;
}

// envLabel is the human-readable label shown in the dropdown.
export function envLabel(t: SyncTask): string {
  const src = gitlabSourceOf(t);
  return t.sourcePath ? `${src} - ${t.sourcePath}` : src;
}

export interface EnvOption {
  label: string;
  value: string; // a representative task id for this path
}

// buildEnvOptions deduplicates tasks by GitLab repo + path, keeping the first
// task seen for each unique path as the representative (used for the backend call
// and for permission resolution).
export function buildEnvOptions(tasks: SyncTask[]): EnvOption[] {
  const seen = new Set<string>();
  const options: EnvOption[] = [];
  for (const t of tasks) {
    const key = envKey(t);
    if (seen.has(key)) continue;
    seen.add(key);
    options.push({ label: envLabel(t), value: t.id });
  }
  // Sort by label for a stable, readable list.
  options.sort((a, b) => a.label.localeCompare(b.label));
  return options;
}
