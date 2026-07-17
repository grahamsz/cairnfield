import type { APIToken, Asset, BackupExport, Bootstrap, EncryptionKey, FolderRecord, MoodboardItem, Note, NoteDetail, NoteSummary, NoteVersion, Share, SyncBootstrap, Template, User } from "./types";
import { appURL } from "./base";

type PageResponse = { notes: NoteSummary[]; page: number; page_size: number; has_more: boolean };

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  let data: Record<string, unknown> = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      if (!res.ok) throw new ApiError(res.status, text || res.statusText);
      throw new Error("Invalid JSON");
    }
  }
  if (!res.ok) throw new ApiError(res.status, typeof data.error === "string" ? data.error : res.statusText);
  return data as T;
}

async function getJSON<T>(url: string): Promise<T> {
  return parse<T>(await fetch(appURL(url), { headers: { Accept: "application/json" } }));
}

async function postJSON<T>(url: string, csrf: string, body: unknown = {}): Promise<T> {
  return parse<T>(await fetch(appURL(url), { method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(body) }));
}

async function putJSON<T>(url: string, csrf: string, body: unknown): Promise<T> {
  return parse<T>(await fetch(appURL(url), { method: "PUT", headers: { Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(body) }));
}

async function deleteJSON<T>(url: string, csrf: string): Promise<T> {
  return parse<T>(await fetch(appURL(url), { method: "DELETE", headers: { Accept: "application/json", "X-CSRF-Token": csrf } }));
}

export const api = {
  bootstrap: () => getJSON<Bootstrap>("/api/bootstrap"),
  setup: (csrf: string, body: { email: string; name: string; password: string }) => postJSON<{ ok: boolean }>("/api/setup", csrf, body),
  login: (csrf: string, body: { email: string; password: string }) => postJSON<{ ok: boolean }>("/api/login", csrf, body),
  logout: (csrf: string) => postJSON<{ ok: boolean }>("/api/logout", csrf),
  adminUsers: () => getJSON<{ users: User[] }>("/api/admin/users"),
  users: () => getJSON<{ users: User[] }>("/api/users"),
  updateProfile: (csrf: string, body: { date_format: string; theme?: string }) => putJSON<{ user: User }>("/api/profile", csrf, body),
  tokens: () => getJSON<{ tokens: APIToken[] }>("/api/tokens"),
  createToken: (csrf: string, name: string) => postJSON<{ token: APIToken; raw_token: string }>("/api/tokens", csrf, { name }),
  revokeToken: (csrf: string, id: number) => deleteJSON<{ ok: boolean }>(`/api/tokens/${id}`, csrf),
  extensionZipURL: () => appURL("/api/extension/zip"),
  createUser: (csrf: string, body: { email: string; name: string; password: string; is_admin: boolean }) => postJSON<{ user: User }>("/api/admin/users", csrf, body),
  notes: (folder = "", page = 1, descendants = false) => getJSON<PageResponse>(`/api/notes?${new URLSearchParams({ folder, page: String(page), ...(descendants ? { descendants: "1" } : {}) })}`),
  trash: (page = 1) => getJSON<PageResponse>(`/api/notes?${new URLSearchParams({ trash: "1", page: String(page) })}`),
  starred: (page = 1) => getJSON<PageResponse>(`/api/notes?${new URLSearchParams({ starred: "1", page: String(page) })}`),
  createNote: (csrf: string, templateID = 0, selectedFolder = "/") => postJSON<{ note: Note; version: NoteVersion; reused: boolean }>("/api/notes", csrf, { template_id: templateID, selected_folder: selectedFolder }),
  note: (id: number | string) => getJSON<NoteDetail>(`/api/notes/${id}`),
  saveNote: (csrf: string, id: number, body: { title: string; folder_path: string; content: string; header_json: string; base_version_id: number; client_id: string; is_encrypted: boolean; autosave?: boolean }) =>
    putJSON<{ note: Note; version: NoteVersion; conflict: boolean }>(`/api/notes/${id}`, csrf, body),
  folders: () => getJSON<{ folders: FolderRecord[] }>("/api/folders"),
  createFolder: (csrf: string, path: string) => postJSON<{ folder: FolderRecord }>("/api/folders", csrf, { path }),
  moveFolder: (csrf: string, source: string, targetParent: string) => postJSON<{ folders: FolderRecord[] }>("/api/folders/move", csrf, { source, target_parent: targetParent }),
  setFolderMode: (csrf: string, path: string, mode: "list" | "gallery" | "moodboard", sortMode = "newest") => postJSON<{ folder: FolderRecord }>("/api/folders/mode", csrf, { path, mode, sort_mode: sortMode }),
  moodboard: (folder: string, descendants = false) => getJSON<{ items: MoodboardItem[]; folder: string }>(`/api/moodboard?${new URLSearchParams({ folder, ...(descendants ? { descendants: "1" } : {}) })}`),
  saveMoodboardOrder: (csrf: string, folder: string, noteIDs: number[]) => postJSON<{ items: MoodboardItem[] }>("/api/moodboard/order", csrf, { folder, note_ids: noteIDs }),
  moveNote: (csrf: string, id: number, folderPath: string) => postJSON<{ note: Note; version: NoteVersion }>(`/api/notes/${id}/folder`, csrf, { folder_path: folderPath }),
  starNote: (csrf: string, id: number, starred: boolean) => postJSON<{ note: Note; version: NoteVersion }>(`/api/notes/${id}/star`, csrf, { starred }),
  trashNote: (csrf: string, id: number) => postJSON<{ note: Note; version: NoteVersion }>(`/api/notes/${id}/trash`, csrf),
  untrashNote: (csrf: string, id: number) => postJSON<{ note: Note; version: NoteVersion }>(`/api/notes/${id}/untrash`, csrf),
  wipeNote: (csrf: string, id: number) => deleteJSON<{ ok: boolean }>(`/api/notes/${id}/wipe`, csrf),
  versions: (id: number) => getJSON<{ versions: NoteVersion[] }>(`/api/notes/${id}/versions`),
  restore: (csrf: string, id: number, versionID: number) => postJSON<{ note: Note; version: NoteVersion }>(`/api/notes/${id}/restore`, csrf, { version_id: versionID }),
  share: (csrf: string, id: number, body: { email: string; permission: "read" | "write" }) => postJSON<{ ok: boolean }>(`/api/notes/${id}/share`, csrf, body),
  removeShare: (csrf: string, id: number, userID: number) => deleteJSON<{ ok: boolean }>(`/api/notes/${id}/share/${userID}`, csrf),
  templates: () => getJSON<{ templates: Template[] }>("/api/templates"),
  saveTemplate: (csrf: string, template: Partial<Template>) =>
    template.id ? putJSON<{ template: Template }>(`/api/templates/${template.id}`, csrf, template) : postJSON<{ template: Template }>("/api/templates", csrf, template),
  deleteTemplate: (csrf: string, id: number) => deleteJSON<{ ok: boolean }>(`/api/templates/${id}`, csrf),
  uploadAsset: async (csrf: string, file: File, noteID = 0, encrypted = false, contentType = "") => {
    const form = new FormData();
    form.set("file", file);
    form.set("note_id", String(noteID));
    if (encrypted) form.set("encrypted", "1");
    if (contentType) form.set("content_type", contentType);
    return parse<{ asset: Asset; url: string }>(await fetch(appURL("/api/assets"), { method: "POST", headers: { Accept: "application/json", "X-CSRF-Token": csrf }, body: form }));
  },
  importNotes: async (csrf: string, file: File, folderPath = "/", preview?: File) => {
    const form = new FormData();
    form.set("file", file);
    form.set("folder_path", folderPath);
    if (preview) form.set("preview", preview);
    return parse<{ notes: Note[]; count: number }>(await fetch(appURL("/api/import"), { method: "POST", headers: { Accept: "application/json", "X-CSRF-Token": csrf }, body: form }));
  },
  clipUrl: (csrf: string, body: { url: string; folder_path?: string; title?: string }) => postJSON<{ note: Note; version: NoteVersion; asset: Asset; url: string; clip_warning?: string }>("/api/clip/url", csrf, body),
  keys: () => getJSON<{ keys: EncryptionKey[] }>("/api/keys"),
  saveKey: (csrf: string, key: Partial<EncryptionKey>) => postJSON<{ key: EncryptionKey }>("/api/keys", csrf, key),
  setDefaultKey: (csrf: string, id: number) => postJSON<{ keys: EncryptionKey[] }>(`/api/keys/${id}/default`, csrf),
  backups: () => getJSON<{ backups: BackupExport[] }>("/api/backups"),
  startBackup: (csrf: string) => postJSON<{ backup: BackupExport }>("/api/backups", csrf),
  search: (q: string, page = 1) => getJSON<PageResponse & { query: string }>(`/api/search?${new URLSearchParams({ q, page: String(page) })}`),
  syncBootstrap: () => getJSON<SyncBootstrap>("/api/sync/bootstrap"),
  syncPush: (csrf: string, edits: unknown[]) => postJSON<{ results: unknown[] }>("/api/sync/push", csrf, { edits })
};

export function messageFromError(err: unknown) {
  return err instanceof Error ? err.message : "Something went wrong";
}
