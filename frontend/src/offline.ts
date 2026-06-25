import type { Bootstrap, FolderRecord, Note, NoteSummary, NoteVersion, SyncBootstrap, SyncNote } from "./types";

export type PendingEdit = {
  op?: "update";
  note_id: number;
  base_version_id: number;
  title: string;
  folder_path: string;
  content: string;
  header_json: string;
  client_id: string;
  is_encrypted: boolean;
  autosave?: boolean;
  queued_at?: number;
};

export type PendingCreate = {
  op: "create";
  note_id: number;
  base_version_id: 0;
  title: string;
  folder_path: string;
  content: string;
  header_json: string;
  client_id: string;
  is_encrypted: boolean;
  autosave?: boolean;
  queued_at?: number;
};

export type PendingOperation = PendingEdit | PendingCreate;

const DB_NAME = "cairnfield-offline-v1";
const DB_VERSION = 3;
const EDITS_STORE = "edits";
const PGP_STORE = "pgp_keys";
const NOTES_STORE = "notes";
const FOLDERS_STORE = "folders";
const META_STORE = "meta";

function openDB(retry = true): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      if (!req.result.objectStoreNames.contains(EDITS_STORE)) req.result.createObjectStore(EDITS_STORE, { keyPath: "client_id" });
      if (!req.result.objectStoreNames.contains(PGP_STORE)) req.result.createObjectStore(PGP_STORE, { keyPath: "id" });
      if (!req.result.objectStoreNames.contains(NOTES_STORE)) req.result.createObjectStore(NOTES_STORE, { keyPath: "note.id" });
      if (!req.result.objectStoreNames.contains(FOLDERS_STORE)) req.result.createObjectStore(FOLDERS_STORE, { keyPath: "path" });
      if (!req.result.objectStoreNames.contains(META_STORE)) req.result.createObjectStore(META_STORE, { keyPath: "key" });
    };
    req.onerror = () => reject(req.error);
    req.onsuccess = () => {
      const db = req.result;
      const required = [EDITS_STORE, PGP_STORE, NOTES_STORE, FOLDERS_STORE, META_STORE];
      if (required.every((store) => db.objectStoreNames.contains(store))) {
        resolve(db);
        return;
      }
      db.close();
      if (!retry) {
        reject(new Error("Offline database schema is missing object stores."));
        return;
      }
      const del = indexedDB.deleteDatabase(DB_NAME);
      del.onerror = () => reject(del.error);
      del.onsuccess = () => openDB(false).then(resolve, reject);
    };
  });
}

function txDone(tx: IDBTransaction) {
  return new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function queueEdit(edit: PendingOperation) {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(EDITS_STORE, "readwrite");
    tx.objectStore(EDITS_STORE).put({ ...edit, queued_at: edit.queued_at || Date.now() });
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function pendingEdits(): Promise<PendingOperation[]> {
  const db = await openDB();
  const edits = await new Promise<PendingOperation[]>((resolve, reject) => {
    const tx = db.transaction(EDITS_STORE, "readonly");
    const req = tx.objectStore(EDITS_STORE).getAll();
    req.onsuccess = () => resolve(req.result as PendingOperation[]);
    req.onerror = () => reject(req.error);
  });
  db.close();
  return edits.sort((a, b) => (a.queued_at || 0) - (b.queued_at || 0));
}

export async function clearPendingEdits() {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(EDITS_STORE, "readwrite");
    tx.objectStore(EDITS_STORE).clear();
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function removePendingEdits(clientIDs: string[]) {
  if (clientIDs.length === 0) return;
  const db = await openDB();
  const tx = db.transaction(EDITS_STORE, "readwrite");
  const store = tx.objectStore(EDITS_STORE);
  clientIDs.forEach((id) => store.delete(id));
  await txDone(tx);
  db.close();
}

export async function saveOfflineBootstrap(bootstrap: Bootstrap) {
  const db = await openDB();
  const tx = db.transaction(META_STORE, "readwrite");
  tx.objectStore(META_STORE).put({ key: "bootstrap", value: bootstrap });
  await txDone(tx);
  db.close();
}

export async function loadOfflineBootstrap(): Promise<Bootstrap | null> {
  const db = await openDB();
  const value = await new Promise<{ value?: Bootstrap } | undefined>((resolve, reject) => {
    const tx = db.transaction(META_STORE, "readonly");
    const req = tx.objectStore(META_STORE).get("bootstrap");
    req.onsuccess = () => resolve(req.result as { value?: Bootstrap } | undefined);
    req.onerror = () => reject(req.error);
  });
  db.close();
  return value?.value || null;
}

export async function saveOfflineSync(data: SyncBootstrap) {
  const db = await openDB();
  const tx = db.transaction([NOTES_STORE, FOLDERS_STORE, META_STORE], "readwrite");
  const notes = tx.objectStore(NOTES_STORE);
  const folders = tx.objectStore(FOLDERS_STORE);
  notes.clear();
  folders.clear();
  (data.notes || []).forEach((item) => notes.put(item));
  (data.folders || []).forEach((folder) => folders.put(folder));
  tx.objectStore(META_STORE).put({ key: "server_time", value: data.server_time || "" });
  await txDone(tx);
  db.close();
}

export async function replaceOfflineFolders(nextFolders: FolderRecord[]) {
  const db = await openDB();
  const tx = db.transaction(FOLDERS_STORE, "readwrite");
  const folders = tx.objectStore(FOLDERS_STORE);
  folders.clear();
  (nextFolders || []).forEach((folder) => folders.put(folder));
  await txDone(tx);
  db.close();
}

export async function upsertOfflineNote(note: Note, version: NoteVersion) {
  const db = await openDB();
  const tx = db.transaction(NOTES_STORE, "readwrite");
  tx.objectStore(NOTES_STORE).put({ note, version });
  await txDone(tx);
  db.close();
}

export async function deleteOfflineNote(id: number) {
  const db = await openDB();
  const tx = db.transaction(NOTES_STORE, "readwrite");
  tx.objectStore(NOTES_STORE).delete(id);
  await txDone(tx);
  db.close();
}

export async function upsertOfflineFolder(folder: FolderRecord) {
  const db = await openDB();
  const tx = db.transaction(FOLDERS_STORE, "readwrite");
  tx.objectStore(FOLDERS_STORE).put(folder);
  await txDone(tx);
  db.close();
}

export async function loadOfflineSync(): Promise<SyncBootstrap> {
  const db = await openDB();
  const result = await new Promise<SyncBootstrap>((resolve, reject) => {
    const tx = db.transaction([NOTES_STORE, FOLDERS_STORE, META_STORE], "readonly");
    const notesReq = tx.objectStore(NOTES_STORE).getAll();
    const foldersReq = tx.objectStore(FOLDERS_STORE).getAll();
    const timeReq = tx.objectStore(META_STORE).get("server_time");
    tx.oncomplete = () => resolve({
      notes: notesReq.result as SyncNote[],
      folders: foldersReq.result as FolderRecord[],
      server_time: ((timeReq.result as { value?: string } | undefined)?.value || "")
    });
    tx.onerror = () => reject(tx.error);
  });
  db.close();
  return result;
}

export async function offlineNote(id: number | string): Promise<SyncNote | null> {
  const data = await loadOfflineSync();
  const key = String(id);
  return (data.notes || []).find((item) => String(item.note.id) === key || item.note.slug === key) || null;
}

export function noteSummaryFromSync(item: SyncNote): NoteSummary {
  return { ...item.note, preview: item.note.is_encrypted ? "Encrypted note" : item.version.content };
}

export async function saveBrowserPGPKey(id: number, privateKeyArmored: string) {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(PGP_STORE, "readwrite");
    tx.objectStore(PGP_STORE).put({ id, privateKeyArmored });
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function loadBrowserPGPKey(id: number): Promise<string> {
  const db = await openDB();
  const value = await new Promise<{ privateKeyArmored?: string } | undefined>((resolve, reject) => {
    const tx = db.transaction(PGP_STORE, "readonly");
    const req = tx.objectStore(PGP_STORE).get(id);
    req.onsuccess = () => resolve(req.result as { privateKeyArmored?: string } | undefined);
    req.onerror = () => reject(req.error);
  });
  db.close();
  return value?.privateKeyArmored || "";
}
