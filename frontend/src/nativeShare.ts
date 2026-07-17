import { appURL } from "./base";

const MAX_SHARE_FILES = 16;
const MAX_SHARE_BYTES = 80 * 1024 * 1024;

type SharedFileEntry = { name?: unknown; type?: unknown; size?: unknown; url?: unknown };
type SharedFilesManifest = { shareId?: unknown; files?: unknown };

function bridge() {
  return (window as any).cairnfieldAndroid;
}

export function nativeShareAvailable() {
  return typeof bridge()?.getSharedFilesManifest === "function";
}

export async function loadSharedFiles(shareId: string): Promise<File[]> {
  try {
    const raw = bridge()?.getSharedFilesManifest?.(shareId);
    if (typeof raw !== "string" || !raw) throw new Error("These shared files are no longer available. Share them again from the other app.");
    let manifest: SharedFilesManifest;
    try {
      manifest = JSON.parse(raw);
    } catch {
      throw new Error("The shared files could not be read. Share them again from the other app.");
    }
    if (manifest.shareId !== shareId) throw new Error("The shared files session does not match this link.");
    const entries = Array.isArray(manifest.files) ? manifest.files as SharedFileEntry[] : [];
    if (entries.length === 0) throw new Error("This share did not include any files.");
    if (entries.length > MAX_SHARE_FILES) throw new Error(`Too many shared files (at most ${MAX_SHARE_FILES}).`);
    const prefix = appURL(`/cairnfield-native-share/${shareId}/`);
    const files: File[] = [];
    let total = 0;
    for (const entry of entries) {
      const name = typeof entry.name === "string" && entry.name.trim() ? entry.name : "shared-file";
      const url = sharedFileURL(entry, name, prefix);
      const declared = Number(entry.size);
      if (Number.isFinite(declared) && declared > MAX_SHARE_BYTES - total) throw new Error("These shared files are larger than the 80 MB limit.");
      const res = await fetch(url, { cache: "no-store" });
      if (!res.ok) throw new Error(`Could not load ${name} (${res.status}).`);
      const bytes = await res.arrayBuffer();
      total += bytes.byteLength;
      if (total > MAX_SHARE_BYTES) throw new Error("These shared files are larger than the 80 MB limit.");
      const type = typeof entry.type === "string" && entry.type ? entry.type : "application/octet-stream";
      files.push(new File([bytes], name, { type }));
    }
    return files;
  } finally {
    try {
      bridge()?.releaseShare?.(shareId);
    } catch {
      // Releasing the native share slot is best-effort.
    }
    stripShareParam();
  }
}

function sharedFileURL(entry: SharedFileEntry, name: string, prefix: string) {
  if (typeof entry.url !== "string" || !entry.url) throw new Error(`The shared file ${name} has no download URL.`);
  let parsed: URL;
  try {
    parsed = new URL(entry.url, window.location.href);
  } catch {
    throw new Error(`The shared file ${name} has an invalid download URL.`);
  }
  if (parsed.origin !== window.location.origin || !parsed.pathname.startsWith(prefix)) {
    throw new Error(`The shared file ${name} has an unexpected download URL.`);
  }
  return parsed.toString();
}

function stripShareParam() {
  try {
    const url = new URL(window.location.href);
    if (!url.searchParams.has("android_share")) return;
    url.searchParams.delete("android_share");
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  } catch {
    // URL cleanup is best-effort.
  }
}
