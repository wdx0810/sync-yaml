/** Simple YAML-like stringifier for key-value maps. */
const yaml = {
  stringify(data: Record<string, string> | null | undefined): string {
    if (!data) return '';
    return Object.entries(data)
      .map(([k, v]) => `${k}: ${v}`)
      .join('\n');
  },
};

export default yaml;
