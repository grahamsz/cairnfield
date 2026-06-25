export const APP_BASE_PATH = normalizeBasePath(basePathFromDocument());

function basePathFromDocument() {
  const href = document.querySelector("base[href]")?.getAttribute("href") || "/";
  try {
    return new URL(href, window.location.origin).pathname;
  } catch {
    return "/";
  }
}

function normalizeBasePath(value: string) {
  const clean = `/${(value || "").trim().replace(/^\/+|\/+$/g, "")}`;
  return clean === "/" ? "" : clean;
}

export function appURL(path: string) {
  if (!path) return APP_BASE_PATH || "/";
  if (/^(https?:|mailto:|data:|blob:)/i.test(path)) return path;
  const suffix = path.startsWith("/") ? path : `/${path}`;
  if (APP_BASE_PATH && (suffix === APP_BASE_PATH || suffix.startsWith(`${APP_BASE_PATH}/`))) return suffix;
  return `${APP_BASE_PATH}${suffix}` || "/";
}

export function appPathname() {
  const path = window.location.pathname || "/";
  if (!APP_BASE_PATH) return path;
  if (path === APP_BASE_PATH) return "/";
  if (path.startsWith(`${APP_BASE_PATH}/`)) return path.slice(APP_BASE_PATH.length) || "/";
  return path;
}
