type PendingEdit = {
  note_id: number;
  base_version_id: number;
  title: string;
  folder_path: string;
  content: string;
  header_json: string;
  client_id: string;
  is_encrypted: boolean;
  autosave?: boolean;
};

const DB_NAME = "cairnfield-offline-v1";
const DB_VERSION = 2;
const STORE = "edits";
const PGP_STORE = "pgp_keys";

function openDB(retry = true): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      if (!req.result.objectStoreNames.contains(STORE)) req.result.createObjectStore(STORE, { keyPath: "client_id" });
      if (!req.result.objectStoreNames.contains(PGP_STORE)) req.result.createObjectStore(PGP_STORE, { keyPath: "id" });
    };
    req.onerror = () => reject(req.error);
    req.onsuccess = () => {
      const db = req.result;
      if (db.objectStoreNames.contains(STORE) && db.objectStoreNames.contains(PGP_STORE)) {
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

export async function queueEdit(edit: PendingEdit) {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).put(edit);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function pendingEdits(): Promise<PendingEdit[]> {
  const db = await openDB();
  const edits = await new Promise<PendingEdit[]>((resolve, reject) => {
    const tx = db.transaction(STORE, "readonly");
    const req = tx.objectStore(STORE).getAll();
    req.onsuccess = () => resolve(req.result as PendingEdit[]);
    req.onerror = () => reject(req.error);
  });
  db.close();
  return edits;
}

export async function clearPendingEdits() {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).clear();
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  db.close();
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
