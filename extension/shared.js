export async function getConfig() {
  const sync = await chrome.storage.sync.get({ baseUrl: "", lastDestination: "", lastDestinationKind: "folder" });
  const local = await chrome.storage.local.get({ token: "" });
  return {
    baseUrl: normalizeBaseUrl(sync.baseUrl),
    token: String(local.token || ""),
    lastDestination: String(sync.lastDestination || ""),
    lastDestinationKind: sync.lastDestinationKind === "board" ? "board" : "folder"
  };
}

export async function saveConfig({ baseUrl, token }) {
  if (baseUrl !== undefined) await chrome.storage.sync.set({ baseUrl: normalizeBaseUrl(baseUrl) });
  if (token !== undefined) await chrome.storage.local.set({ token });
}

export async function saveDestination(folderPath, destinationKind) {
  await chrome.storage.sync.set({ lastDestination: folderPath, lastDestinationKind: destinationKind });
}

export function normalizeBaseUrl(value) {
  return String(value || "").trim().replace(/\/+$/, "");
}

export async function cairnfieldFetch(path, init = {}) {
  const config = await getConfig();
  if (!config.baseUrl || !config.token) throw new Error("Configure Cairnfield URL and API token first.");
  const url = `${config.baseUrl}${path}`;
  const headers = new Headers(init.headers || {});
  headers.set("Authorization", `Bearer ${config.token}`);
  headers.set("Accept", "application/json");
  const res = await fetch(url, { ...init, headers });
  const text = await res.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      if (!res.ok) throw new Error(text || res.statusText);
      throw new Error("Cairnfield returned invalid JSON.");
    }
  }
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

export async function bootstrap() {
  return cairnfieldFetch("/api/clip/bootstrap");
}

export function originPattern(baseUrl) {
  const url = new URL(baseUrl);
  return `${url.origin}/*`;
}

export function extensionNow() {
  return new Date().toISOString();
}
