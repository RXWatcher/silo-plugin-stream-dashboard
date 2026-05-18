export function adminBackTarget(pathname: string): { href: string; label: string; title: string } {
  if (pathname.includes("/admin")) {
    return {
      href: "/admin/plugins",
      label: "Plugins",
      title: "Back to Continuum plugins",
    };
  }
  return {
    href: "/",
    label: "Continuum",
    title: "Back to Continuum",
  };
}
