export function extractMountPath(pathname: string): string {
  const match = pathname.match(/^(\/api\/v1\/plugins\/[^/]+)/);
  return match ? match[1] : "";
}

export function pluginMountPath(): string {
  return extractMountPath(window.location.pathname);
}
