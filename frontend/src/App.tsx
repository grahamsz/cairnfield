import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  CaretDownIcon,
  CaretRightIcon,
  CheckIcon,
  ClockCounterClockwiseIcon,
  CodeIcon,
  DownloadSimpleIcon,
  DotsSixVerticalIcon,
  FilePlusIcon,
  FileTextIcon,
  FloppyDiskIcon,
  FolderPlusIcon,
  FolderIcon,
  GearIcon,
  LockOpenIcon,
  KeyIcon,
  ListIcon,
  LockIcon,
  MagnifyingGlassIcon,
  PencilSimpleIcon,
  TrashIcon,
  UploadSimpleIcon,
  StarIcon,
  ShareNetworkIcon,
  SignOutIcon,
  UsersIcon
} from "@phosphor-icons/react";
import {
  BlockTypeSelect,
  BoldItalicUnderlineToggles,
  CodeToggle,
  CreateLink,
  headingsPlugin,
  imagePlugin,
  InsertImage,
  InsertTable,
  InsertThematicBreak,
  linkDialogPlugin,
  linkPlugin,
  listsPlugin,
  ListsToggle,
  markdownShortcutPlugin,
  MDXEditor,
  type MDXEditorMethods,
  quotePlugin,
  Separator,
  tablePlugin,
  thematicBreakPlugin,
  toolbarPlugin,
  UndoRedo
} from "@mdxeditor/editor";
import "@mdxeditor/editor/style.css";
import { api, messageFromError } from "./api";
import { decryptBytes, decryptText, downloadKey, encryptBytes, encryptText, generateKey, passphraseIssues, privateKeyMetadata, verifyPrivateKey } from "./crypto";
import { clearPendingEdits, loadBrowserPGPKey, pendingEdits, queueEdit, saveBrowserPGPKey } from "./offline";
import type { AuthProvider, BackupExport, Bootstrap, EncryptionKey, FolderRecord, Note, NoteSummary, NoteVersion, Share, Template, User } from "./types";

type Toast = { id: number; message: string; kind: "success" | "error" | "loading" };
type View = "editor" | "folder" | "search" | "settings" | "admin";
type EditorMode = "rich" | "raw" | "history" | "share";
type SaveCause = "auto" | "manual";
type FolderNode = { path: string; name: string; children: FolderNode[]; notes: NoteSummary[]; noteCount: number };
type ImportRequest = { files: File[]; folderPath: string };
type SecurityUnlock = { keyID: number; label: string; fingerprint: string; publicKeyArmored: string; privateKeyArmored: string; passphrase: string; unlockedUntil: number };
type EditorSnapshot = { activeNote: Note; version: NoteVersion; title: string; folder: string; content: string; encrypted: boolean; plainUnlocked: boolean; securityUnlock: SecurityUnlock | null; defaultKey: EncryptionKey | null; signature: string };
type DateFormatOption = { value: string; label: string; sample: string };

let toastSeq = 1;
const appLockChannelName = "cairnfield-lock";
const dateFormatOptions: DateFormatOption[] = [
  { value: "ymd_slash", label: "YYYY/MM/DD", sample: "2026/06/24" },
  { value: "mdy_slash", label: "MM/DD/YYYY", sample: "06/24/2026" },
  { value: "dmy_slash", label: "DD/MM/YYYY", sample: "24/06/2026" },
  { value: "iso", label: "YYYY-MM-DD", sample: "2026-06-24" },
  { value: "long", label: "Localized long", sample: "Jun 24, 2026" }
];

export default function App() {
  const [bootstrap, setBootstrap] = useState<Bootstrap | null>(null);
  const [notes, setNotes] = useState<NoteSummary[]>([]);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [activeNote, setActiveNote] = useState<Note | null>(null);
  const [version, setVersion] = useState<NoteVersion | null>(null);
  const [shares, setShares] = useState<Share[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [keys, setKeys] = useState<EncryptionKey[]>([]);
  const [folder, setFolder] = useState("");
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<NoteSummary[]>([]);
  const [searchPage, setSearchPage] = useState(1);
  const [searchHasMore, setSearchHasMore] = useState(false);
  const [folderNotes, setFolderNotes] = useState<NoteSummary[]>([]);
  const [folderPage, setFolderPage] = useState(1);
  const [folderHasMore, setFolderHasMore] = useState(false);
  const [view, setView] = useState<View>("editor");
  const [mobileOpen, setMobileOpen] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(256);
  const [selectedNoteIDs, setSelectedNoteIDs] = useState<Set<number>>(() => new Set());
  const [templateEditID, setTemplateEditID] = useState(0);
  const [expandedFolders, setExpandedFolders] = useState<Set<string>>(() => new Set(["/"]));
  const [importRequest, setImportRequest] = useState<ImportRequest | null>(null);
  const [securityUnlock, setSecurityUnlock] = useState<SecurityUnlock | null>(null);
  const [securityUnlockOpen, setSecurityUnlockOpen] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const decryptedTitleCache = useRef<Map<number, string>>(new Map());

  const csrf = bootstrap?.csrf || "";
  const user = bootstrap?.user || null;
  const dateFormat = user?.date_format || "ymd_slash";
  const defaultKey = useMemo(() => keys.find((key) => key.is_default) || keys[0] || null, [keys]);
  const unlocked = securityUnlock && securityUnlock.unlockedUntil > Date.now() ? securityUnlock : null;

  const addToast = useCallback((message: string, kind: Toast["kind"] = "success") => {
    const id = toastSeq++;
    setToasts((items) => [...items, { id, message, kind }]);
    if (kind !== "loading") window.setTimeout(() => setToasts((items) => items.filter((t) => t.id !== id)), 4200);
    return id;
  }, []);

  const lockApp = useCallback((broadcast = true) => {
    setSecurityUnlock(null);
    setSecurityUnlockOpen(false);
    if (broadcast) broadcastAppLock();
  }, []);

  const refreshBootstrap = useCallback(async () => {
    const data = await api.bootstrap();
    setBootstrap(data);
    setTemplates(data.templates || []);
  }, []);

  const updateCurrentUser = useCallback((nextUser: User) => {
    setBootstrap((current) => current ? { ...current, user: nextUser } : current);
  }, []);

  const refreshKeys = useCallback(async () => {
    if (!user) return;
    const data = await api.keys();
    setKeys(data.keys || []);
  }, [user]);

  useEffect(() => {
    if (!securityUnlock?.unlockedUntil) return;
    const delay = Math.max(0, securityUnlock.unlockedUntil - Date.now());
    const timer = window.setTimeout(() => lockApp(true), delay);
    return () => window.clearTimeout(timer);
  }, [lockApp, securityUnlock?.unlockedUntil]);

  useEffect(() => {
    const onServiceWorkerMessage = (event: MessageEvent) => {
      if (event.data?.type === "CAIRNFIELD_LOCK_APP") lockApp(false);
    };
    navigator.serviceWorker?.addEventListener("message", onServiceWorkerMessage);
    let channel: BroadcastChannel | null = null;
    if ("BroadcastChannel" in window) {
      channel = new BroadcastChannel(appLockChannelName);
      channel.onmessage = (event) => {
        if (event.data?.type === "CAIRNFIELD_LOCK_APP") lockApp(false);
      };
    }
    return () => {
      navigator.serviceWorker?.removeEventListener("message", onServiceWorkerMessage);
      channel?.close();
    };
  }, [lockApp]);

  useEffect(() => {
    if (!unlocked) return;
    let cancelled = false;
    void decryptSummaryTitles(notes, unlocked, decryptedTitleCache.current).then((next) => {
      if (!cancelled && next !== notes) setNotes(next);
    }).catch(() => undefined);
    void decryptSummaryTitles(folderNotes, unlocked, decryptedTitleCache.current).then((next) => {
      if (!cancelled && next !== folderNotes) setFolderNotes(next);
    }).catch(() => undefined);
    void decryptSummaryTitles(searchResults, unlocked, decryptedTitleCache.current).then((next) => {
      if (!cancelled && next !== searchResults) setSearchResults(next);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, [folderNotes, notes, searchResults, unlocked]);

  const openNote = useCallback(async (id: number | string, historyMode: "push" | "replace" | "none" = "push") => {
    try {
      const data = await api.note(id);
      let note = data.note;
      let noteVersion = data.version;
      if (unlocked && note.is_encrypted) {
        const cachedTitle = decryptedTitleCache.current.get(note.id);
        const title = cachedTitle || (looksEncrypted(note.title) ? await decryptText(note.title, unlocked.privateKeyArmored, unlocked.passphrase) : note.title);
        const content = looksEncrypted(noteVersion.content) ? await decryptText(noteVersion.content, unlocked.privateKeyArmored, unlocked.passphrase) : noteVersion.content;
        if (title) decryptedTitleCache.current.set(note.id, title);
        note = { ...note, title: title || "Untitled" };
        noteVersion = { ...noteVersion, content: content || "" };
        setNotes((items) => preserveUnlockedTitles(items, decryptedTitleCache.current));
        setFolderNotes((items) => preserveUnlockedTitles(items, decryptedTitleCache.current));
        setSearchResults((items) => preserveUnlockedTitles(items, decryptedTitleCache.current));
      }
      setActiveNote(note);
      setVersion(noteVersion);
      setShares(data.shares || []);
      setView("editor");
      setMobileOpen(false);
      setExpandedFolders((current) => expandAncestors(current, data.note.folder_path || "/"));
      setFolder(data.note.folder_path || "/");
      if (historyMode !== "none") {
        const url = noteURL(data.note);
        if (window.location.pathname !== url) {
          window.history[historyMode === "replace" ? "replaceState" : "pushState"]({ note: data.note.slug }, "", url);
        }
      }
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }, [addToast, unlocked]);

  const executeSearch = useCallback(async (searchQuery: string, historyMode: "push" | "replace" | "none" = "push", page = 1) => {
    const trimmed = searchQuery.trim();
    if (!trimmed) return;
    const data = await api.search(trimmed, page);
    setQuery(trimmed);
    setSearchResults(unlocked ? preserveUnlockedTitles(data.notes || [], decryptedTitleCache.current) : data.notes || []);
    setSearchPage(data.page || page);
    setSearchHasMore(Boolean(data.has_more));
    setSelectedNoteIDs(new Set());
    setView("search");
    setMobileOpen(false);
    if (historyMode !== "none") {
      const url = searchURL(trimmed, data.page || page);
      if (`${window.location.pathname}${window.location.search}` !== url) {
        window.history[historyMode === "replace" ? "replaceState" : "pushState"]({ search: trimmed }, "", url);
      }
    }
  }, [unlocked]);

  const loadNotes = useCallback(async () => {
    if (!user) return;
    const data = await api.notes("");
    const list = unlocked ? preserveUnlockedTitles(data.notes || [], decryptedTitleCache.current) : data.notes || [];
    setNotes(list);
    if (!activeNote) {
      const routeKey = noteKeyFromLocation();
      const routeSearch = searchRouteFromLocation();
      if (routeSearch.query) void executeSearch(routeSearch.query, "replace", routeSearch.page);
      else if (routeKey) void openNote(routeKey, "replace");
      else if (list[0]) void openNote(list[0].id, "replace");
    }
  }, [activeNote, executeSearch, openNote, unlocked, user]);

  const loadFolders = useCallback(async () => {
    if (!user) return;
    const data = await api.folders();
    setFolders(data.folders || []);
  }, [user]);

  useEffect(() => {
    void refreshBootstrap().catch((err) => addToast(messageFromError(err), "error"));
  }, [addToast, refreshBootstrap]);

  useEffect(() => {
    void loadNotes().catch((err) => addToast(messageFromError(err), "error"));
  }, [addToast, loadNotes]);

  useEffect(() => {
    void loadFolders().catch((err) => addToast(messageFromError(err), "error"));
  }, [addToast, loadFolders]);

  useEffect(() => {
    void refreshKeys().catch(() => undefined);
  }, [refreshKeys]);

  useEffect(() => {
    if (!user) return;
    const onPopState = () => {
      const key = noteKeyFromLocation();
      const routeSearch = searchRouteFromLocation();
      if (key) void openNote(key, "none");
      else if (routeSearch.query) void executeSearch(routeSearch.query, "none", routeSearch.page);
      else setView("editor");
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [executeSearch, openNote, user]);

  useEffect(() => {
    if (!user || !csrf) return;
    async function flush() {
      if (!navigator.onLine) return;
      const edits = await pendingEdits();
      if (edits.length === 0) return;
      const res = await api.syncPush(csrf, edits);
      await clearPendingEdits();
      addToast(`Synced ${res.results.length} offline edit${res.results.length === 1 ? "" : "s"}.`);
      await loadNotes();
    }
    window.addEventListener("online", flush);
    void flush().catch(() => undefined);
    return () => window.removeEventListener("online", flush);
  }, [addToast, csrf, loadNotes, user]);

  async function runSearch() {
    await executeSearch(query, "push", 1);
  }

  async function openFolder(path: string, page = 1) {
    try {
      const data = path === "__trash" ? await api.trash(page) : path === "__starred" ? await api.starred(page) : await api.notes(path, page);
      const summaries = unlocked ? preserveUnlockedTitles(data.notes || [], decryptedTitleCache.current) : data.notes || [];
      const list = [...summaries].sort((a, b) => b.updated_at.localeCompare(a.updated_at));
      setFolderNotes(list);
      setSelectedNoteIDs(new Set());
      setFolder(path);
      setFolderPage(data.page || page);
      setFolderHasMore(Boolean(data.has_more));
      setView("folder");
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  if (!bootstrap) return <AuthShell><div className="panel muted">Loading cairnfield...</div></AuthShell>;
  if (!bootstrap.users_exist) return <AuthShell><Setup csrf={csrf} onDone={refreshBootstrap} /></AuthShell>;
  if (!user) return <AuthShell><Login csrf={csrf} authProviders={bootstrap.auth_providers || []} onDone={refreshBootstrap} /></AuthShell>;
  const userID = user.id;

  async function createNote(templateID = 0, encryptedNote = false) {
    try {
      if (encryptedNote && !unlocked) {
        setSecurityUnlockOpen(true);
        addToast("Unlock your PGP key before creating encrypted notes.", "error");
        return;
      }
      if (encryptedNote && !defaultKey) {
        addToast("Create or import a PGP key before creating encrypted notes.", "error");
        return;
      }
      const targetFolder = currentNoteTargetFolder(folder);
      const data = await api.createNote(csrf, templateID, targetFolder);
      let note = data.note;
      let version = data.version;
      if (encryptedNote && data.reused) {
        addToast("Opened existing note.");
      } else if (encryptedNote) {
        const encryptionKey = unlocked?.publicKeyArmored || defaultKey!.public_key_armored;
        const encryptedTitle = await encryptText(data.note.title || "Untitled", encryptionKey);
        const encryptedContent = await encryptText(data.version.content || "", encryptionKey);
        const saved = await api.saveNote(csrf, data.note.id, {
          title: encryptedTitle,
          folder_path: data.note.folder_path || targetFolder,
          content: encryptedContent,
          header_json: data.version.header_json || "{}",
          base_version_id: data.version.id,
          client_id: crypto.randomUUID(),
          is_encrypted: true
        });
        note = { ...saved.note, title: data.note.title };
        version = { ...saved.version, content: data.version.content || "" };
        decryptedTitleCache.current.set(note.id, note.title);
      }
      setNotes((items) => data.reused ? (items || []) : [{ ...note, preview: note.is_encrypted ? "" : version.content }, ...(items || [])]);
      setFolders((items) => upsertFolder(items, { id: 0, user_id: userID, path: note.folder_path, created_at: note.created_at, updated_at: note.updated_at }));
      setActiveNote(note);
      setVersion(version);
      setFolder(note.folder_path || "/");
      setExpandedFolders((current) => expandAncestors(current, note.folder_path || "/"));
      window.history.pushState({ note: note.slug }, "", noteURL(note));
      setView("editor");
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function createFolder(parent: string, name: string) {
    try {
      const path = joinFolderPath(parent, name);
      const data = await api.createFolder(csrf, path);
      setFolders((items) => upsertFolder(items, data.folder));
      setExpandedFolders((current) => expandAncestors(current, path));
      setFolder(path);
      setView("editor");
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function moveNotesToFolder(noteIDs: number[], targetFolder: string) {
    try {
      let latest: { note: Note; version: NoteVersion } | null = null;
      for (const noteID of noteIDs) {
        latest = await api.moveNote(csrf, noteID, targetFolder);
        const moved = latest;
        setNotes((items) => (items || []).map((item) => item.id === noteID ? { ...item, ...moved.note } : item));
        setFolderNotes((items) => (items || []).map((item) => item.id === noteID ? { ...item, ...moved.note } : item));
        if (activeNote?.id === noteID) {
          setActiveNote(moved.note);
          setVersion(moved.version);
          setFolder(moved.note.folder_path || "/");
        }
      }
      if (latest) {
        setFolders((items) => upsertFolder(items, { id: 0, user_id: userID, path: latest.note.folder_path, created_at: latest.note.created_at, updated_at: latest.note.updated_at }));
        setExpandedFolders((current) => expandAncestors(current, latest!.note.folder_path || "/"));
        addToast(`Moved ${noteIDs.length} note${noteIDs.length === 1 ? "" : "s"}.`);
      }
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function moveFolderToFolder(source: string, targetParent: string) {
    const normalizedSource = normalizeFolderPath(source);
    const normalizedTarget = normalizeFolderPath(targetParent);
    const destination = joinFolderPath(normalizedTarget, folderNameFromPath(normalizedSource));
    if (normalizedSource === "/" || normalizedSource === normalizedTarget || normalizedTarget.startsWith(`${normalizedSource}/`)) return;
    if (!window.confirm(`Move ${normalizedSource} into ${normalizedTarget}? It will become ${destination}.`)) return;
    try {
      const data = await api.moveFolder(csrf, normalizedSource, normalizedTarget);
      setFolders(data.folders || []);
      const movePath = (path: string) => replaceFolderPath(path, normalizedSource, destination);
      setNotes((items) => (items || []).map((item) => ({ ...item, folder_path: movePath(item.folder_path || "/") })));
      setFolderNotes((items) => (items || []).map((item) => ({ ...item, folder_path: movePath(item.folder_path || "/") })));
      if (activeNote) {
        const nextPath = movePath(activeNote.folder_path || "/");
        if (nextPath !== activeNote.folder_path) {
          setActiveNote({ ...activeNote, folder_path: nextPath });
          setFolder(nextPath);
        }
      }
      setExpandedFolders((current) => expandAncestors(current, destination));
      addToast(`Moved ${normalizedSource} to ${destination}.`);
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function starDraggedNotes(noteIDs: number[]) {
    if (noteIDs.length === 0) return;
    try {
      for (const noteID of noteIDs) {
        await api.starNote(csrf, noteID, true);
      }
      const markStarred = (items: NoteSummary[]) => (items || []).map((item) => noteIDs.includes(item.id) ? { ...item, is_starred: true } : item);
      setNotes(markStarred);
      setFolderNotes(markStarred);
      setSearchResults(markStarred);
      if (activeNote && noteIDs.includes(activeNote.id)) setActiveNote({ ...activeNote, is_starred: true });
      addToast(`Starred ${noteIDs.length} note${noteIDs.length === 1 ? "" : "s"}.`);
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function trashDraggedNotes(noteIDs: number[]) {
    if (noteIDs.length === 0) return;
    if (!window.confirm(`Move ${noteIDs.length} selected note${noteIDs.length === 1 ? "" : "s"} to Trash?`)) return;
    try {
      for (const noteID of noteIDs) {
        await api.trashNote(csrf, noteID);
      }
      const removeTrashed = (items: NoteSummary[]) => (items || []).filter((item) => !noteIDs.includes(item.id));
      setNotes(removeTrashed);
      setFolderNotes(folder === "__trash" ? (items) => items : removeTrashed);
      setSearchResults(removeTrashed);
      if (activeNote && noteIDs.includes(activeNote.id)) {
        setActiveNote(null);
        setVersion(null);
      }
      addToast(`Moved ${noteIDs.length} note${noteIDs.length === 1 ? "" : "s"} to trash.`);
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function emptyTrash() {
    const ids = folderNotes.map((note) => note.id);
    if (ids.length === 0) return;
    if (!window.confirm(`Permanently delete ${ids.length} trashed note${ids.length === 1 ? "" : "s"}? This cannot be undone.`)) return;
    try {
      for (const id of ids) {
        await api.wipeNote(csrf, id);
      }
      setFolderNotes([]);
      setSelectedNoteIDs(new Set());
      if (activeNote && ids.includes(activeNote.id)) {
        setActiveNote(null);
        setVersion(null);
      }
      addToast("Trash emptied.");
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function loadTrash() {
    try {
      const data = await api.trash();
      setNotes(data.notes || []);
      setFolder("__trash");
      setView("editor");
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function importFiles(files: File[], targetFolder: string) {
    try {
      let count = 0;
      for (const file of files) {
        const result = await api.importNotes(csrf, file, targetFolder);
        count += result.count;
      }
      addToast(`Imported ${count} note${count === 1 ? "" : "s"}.`);
      await loadNotes();
      await loadFolders();
    } catch (err) {
      addToast(messageFromError(err), "error");
    }
  }

  async function logout() {
    await api.logout(csrf);
    setBootstrap({ ...bootstrap!, user: null });
    setActiveNote(null);
    setVersion(null);
  }

  function beginSidebarResize(event: React.PointerEvent<HTMLButtonElement>) {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = sidebarWidth;
    const move = (moveEvent: PointerEvent) => setSidebarWidth(Math.min(480, Math.max(220, startWidth + moveEvent.clientX - startX)));
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  }

  return (
    <>
      <header className="topbar">
        <button type="button" className="mobile-menu-button ghost" onClick={() => setMobileOpen(true)}><ListIcon /></button>
        <button type="button" className="brand ghost" onClick={() => setView("editor")}><span className="brand-mark"><img src="/icon.svg" alt="" /></span><span>cairnfield</span></button>
        <form className="top-search" onSubmit={(event) => { event.preventDefault(); void runSearch().catch((err) => addToast(messageFromError(err), "error")); }}>
          <MagnifyingGlassIcon className="search-icon" />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search notes with title:, path:, tag:, after:, before:" />
        </form>
        <div className="top-actions">
          {keys.length > 0 ? <button type="button" className={`ghost global-lock ${unlocked ? "unlocked" : ""}`} title={unlocked ? "Lock app" : "Unlock app"} aria-label={unlocked ? "Lock app" : "Unlock app"} onClick={() => unlocked ? lockApp(true) : setSecurityUnlockOpen(true)}>{unlocked ? <LockOpenIcon /> : <LockIcon />}</button> : null}
          <NewNoteMenu
            templates={templates}
            createNote={createNote}
            encryptedAvailable={Boolean(defaultKey)}
            securityUnlocked={Boolean(unlocked)}
            openUnlock={() => setSecurityUnlockOpen(true)}
            editTemplate={(templateID) => {
              setTemplateEditID(templateID);
              setView("settings");
            }}
          />
          <button type="button" className="ghost" onClick={() => setView("settings")} title="Settings"><GearIcon /></button>
          {user.is_admin ? <button type="button" className="ghost" onClick={() => setView("admin")} title="Users"><UsersIcon /></button> : null}
          <button type="button" className="ghost" onClick={() => void logout()} title="Logout"><SignOutIcon /></button>
        </div>
      </header>
      <div className="app" style={{ "--sidebar-width": `${sidebarWidth}px` } as React.CSSProperties}>
        {mobileOpen ? <button type="button" className="mobile-sidebar-scrim" onClick={() => setMobileOpen(false)} /> : null}
        <aside className={`sidebar ${mobileOpen ? "open" : ""}`}>
          <div className="sidebar-mobile-head"><strong>cairnfield</strong><button type="button" className="ghost" onClick={() => setMobileOpen(false)}>Close</button></div>
          <FolderBrowser
            folders={folders}
            notes={notes}
            activeNoteID={activeNote?.id || 0}
            selectedFolder={folder}
            expandedFolders={expandedFolders}
            setExpandedFolders={setExpandedFolders}
            setSelectedFolder={(path) => void openFolder(path === "/" ? "" : path)}
            openNote={openNote}
            createFolder={createFolder}
            moveNotesToFolder={moveNotesToFolder}
            moveFolderToFolder={moveFolderToFolder}
            selectedNoteIDs={selectedNoteIDs}
            securityUnlocked={Boolean(unlocked)}
            dateFormat={dateFormat}
            requestImport={(files, folderPath) => setImportRequest({ files, folderPath })}
          />
          <button type="button" className={`nav-item starred-nav ${folder === "__starred" ? "active" : ""}`} onClick={() => void openFolder("__starred")} onDragOver={(event) => event.preventDefault()} onDrop={(event) => {
            event.preventDefault();
            void starDraggedNotes(noteIDsFromDragEvent(event));
          }}><StarIcon />Starred</button>
          <button type="button" className={`nav-item trash-nav ${folder === "__trash" ? "active" : ""}`} onClick={() => void openFolder("__trash")} onDragOver={(event) => event.preventDefault()} onDrop={(event) => {
            event.preventDefault();
            void trashDraggedNotes(noteIDsFromDragEvent(event));
          }}><TrashIcon />Trash</button>
        </aside>
        <button type="button" className="sidebar-resizer" aria-label="Resize sidebar" onPointerDown={beginSidebarResize} />
        <main className="content">
          {view === "settings" ? <SettingsView csrf={csrf} user={user} updateUser={updateCurrentUser} templates={templates} initialTemplateID={templateEditID} setTemplates={setTemplates} keys={keys} setKeys={setKeys} addToast={addToast} /> :
            view === "admin" ? <AdminView csrf={csrf} addToast={addToast} /> :
            view === "search" ? <NoteListView csrf={csrf} title="Search" subtitle={`${searchResults.length.toLocaleString()} result${searchResults.length === 1 ? "" : "s"} on page ${searchPage} for ${query}`} results={searchResults} setResults={setSearchResults} selected={selectedNoteIDs} setSelected={setSelectedNoteIDs} openNote={openNote} securityUnlocked={Boolean(unlocked)} highlight={query} addToast={addToast} page={searchPage} hasMore={searchHasMore} onPageChange={(page) => executeSearch(query, "push", page)} /> :
            view === "folder" ? <NoteListView csrf={csrf} title={folderTitle(folder)} subtitle={`${folderNotes.length.toLocaleString()} note${folderNotes.length === 1 ? "" : "s"} on page ${folderPage}, recently edited first`} results={folderNotes} setResults={setFolderNotes} selected={selectedNoteIDs} setSelected={setSelectedNoteIDs} openNote={openNote} securityUnlocked={Boolean(unlocked)} addToast={addToast} page={folderPage} hasMore={folderHasMore} onPageChange={(page) => openFolder(folder, page)} action={folder === "__trash" ? <button type="button" className="secondary danger-action" disabled={folderNotes.length === 0} onClick={() => void emptyTrash()}><TrashIcon />Empty trash</button> : null} /> :
            <EditorView csrf={csrf} user={user} activeNote={activeNote} version={version} shares={shares} defaultKey={defaultKey} securityUnlock={unlocked} openUnlock={() => setSecurityUnlockOpen(true)} rememberDecryptedTitle={(id, title) => decryptedTitleCache.current.set(id, title)} setActiveNote={setActiveNote} setVersion={setVersion} setShares={setShares} setNotes={setNotes} reloadNotes={loadNotes} addToast={addToast} />}
        </main>
      </div>
      {importRequest ? <ImportApprovalDialog request={importRequest} onCancel={() => setImportRequest(null)} onApprove={() => { const req = importRequest; setImportRequest(null); void importFiles(req.files, req.folderPath); }} /> : null}
      {securityUnlockOpen ? <PGPUnlockDialog keys={keys} onClose={() => setSecurityUnlockOpen(false)} onUnlocked={(state) => { setSecurityUnlock(state); setSecurityUnlockOpen(false); addToast("PGP key unlocked."); }} addToast={addToast} /> : null}
      <ToastStack toasts={toasts} onDismiss={(id) => setToasts((items) => items.filter((t) => t.id !== id))} />
    </>
  );
}

function AuthShell({ children }: { children: React.ReactNode }) {
  return <div className="auth-page"><div className="auth-brand"><span className="brand-mark"><img src="/icon.svg" alt="" /></span>cairnfield</div>{children}</div>;
}

function NewNoteMenu({ templates, createNote, encryptedAvailable, securityUnlocked, openUnlock, editTemplate }: { templates: Template[]; createNote: (templateID?: number, encrypted?: boolean) => Promise<void>; encryptedAvailable: boolean; securityUnlocked: boolean; openUnlock: () => void; editTemplate: (templateID: number) => void }) {
  const [open, setOpen] = useState(false);
  const [encrypted, setEncrypted] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  async function create(templateID = 0) {
    setOpen(false);
    await createNote(templateID, encrypted);
  }

  return (
    <div className="new-note-menu" ref={menuRef}>
      <button type="button" className="secondary" onClick={() => setOpen((value) => !value)} aria-expanded={open}>
        <FilePlusIcon />New<CaretDownIcon />
      </button>
      {open ? (
        <div className="new-note-popover" role="menu">
          <button type="button" className="new-note-row" onClick={() => void create()}>
            <span><FilePlusIcon />Blank note</span>
          </button>
          <label className="new-note-toggle"><input type="checkbox" disabled={!encryptedAvailable} checked={encrypted} onChange={(event) => {
            if (event.target.checked && !securityUnlocked) {
              openUnlock();
              setEncrypted(false);
              return;
            }
            setEncrypted(event.target.checked);
          }} /><LockIcon />Encrypted note</label>
          {(templates || []).map((template) => (
            <div className="new-note-row split" key={template.id}>
              <button type="button" onClick={() => void create(template.id)}><FilePlusIcon />{template.name}</button>
              <button type="button" className="icon-only ghost" title={`Edit ${template.name}`} onClick={() => { setOpen(false); editTemplate(template.id); }}>
                <PencilSimpleIcon />
              </button>
            </div>
          ))}
          <button type="button" className="new-note-row muted-row" onClick={() => { setOpen(false); editTemplate(0); }}>
            <span><GearIcon />Manage templates</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}

function ImportApprovalDialog({ request, onCancel, onApprove }: { request: ImportRequest; onCancel: () => void; onApprove: () => void }) {
  const zipCount = request.files.filter((file) => file.name.toLowerCase().endsWith(".zip")).length;
  return (
    <div className="modal-backdrop" onClick={onCancel}>
      <div className="security-dialog" role="dialog" aria-label="Import notes" onClick={(event) => event.stopPropagation()}>
        <div className="modal-head"><h2>Import notes</h2><button type="button" className="ghost" onClick={onCancel}>Close</button></div>
        <p className="muted">Import {request.files.length} file{request.files.length === 1 ? "" : "s"} into {request.folderPath || "/"}.</p>
        {zipCount > 0 ? <div className="notice subtle">Zip archives can contain many notes. Cairnfield will import markdown files and preserve archive folders under the target folder.</div> : null}
        <div className="import-file-list">{request.files.map((file) => <span key={`${file.name}-${file.size}`}><UploadSimpleIcon />{file.name}</span>)}</div>
        <div className="modal-actions"><button type="button" className="secondary" onClick={onCancel}>Cancel</button><button type="button" onClick={onApprove}>Import</button></div>
      </div>
    </div>
  );
}

function Setup({ csrf, onDone }: { csrf: string; onDone: () => Promise<void> }) {
  return <AuthForm title="Create the first admin" submitLabel="Create admin" onSubmit={(body) => api.setup(csrf, body).then(onDone)} />;
}

function Login({ csrf, authProviders, onDone }: { csrf: string; authProviders: AuthProvider[]; onDone: () => Promise<void> }) {
  return <AuthForm title="Sign in" submitLabel="Sign in" loginOnly authProviders={authProviders} onSubmit={(body) => api.login(csrf, { email: body.email, password: body.password }).then(onDone)} />;
}

function AuthForm({ title, submitLabel, loginOnly = false, authProviders = [], onSubmit }: { title: string; submitLabel: string; loginOnly?: boolean; authProviders?: AuthProvider[]; onSubmit: (body: { email: string; name: string; password: string }) => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  return (
    <form className="auth-card" onSubmit={(event) => { event.preventDefault(); setError(""); void onSubmit({ email, name: name || email, password }).catch((err) => setError(messageFromError(err))); }}>
      <h1>{title}</h1>
      {error ? <div className="error">{error}</div> : null}
      <label>Email<input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoFocus /></label>
      {!loginOnly ? <label>Name<input value={name} onChange={(event) => setName(event.target.value)} /></label> : null}
      <label>Password<input type="password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>
      <button type="submit">{submitLabel}</button>
      {loginOnly && authProviders.length ? (
        <div className="auth-providers">
          {authProviders.map((provider) => <a className="button secondary" href={provider.login_url} key={provider.id}>Sign in with {provider.name}</a>)}
        </div>
      ) : null}
    </form>
  );
}

function EditorView({ csrf, user, activeNote, version, shares, defaultKey, securityUnlock, openUnlock, rememberDecryptedTitle, setActiveNote, setVersion, setShares, setNotes, reloadNotes, addToast }: {
  csrf: string;
  user: User;
  activeNote: Note | null;
  version: NoteVersion | null;
  shares: Share[];
  defaultKey: EncryptionKey | null;
  securityUnlock: SecurityUnlock | null;
  openUnlock: () => void;
  rememberDecryptedTitle: (id: number, title: string) => void;
  setActiveNote: (note: Note | null) => void;
  setVersion: (version: NoteVersion | null) => void;
  setShares: (shares: Share[]) => void;
  setNotes: React.Dispatch<React.SetStateAction<NoteSummary[]>>;
  reloadNotes: () => Promise<void>;
  addToast: (message: string, kind?: Toast["kind"]) => number;
}) {
  const [mode, setMode] = useState<EditorMode>("rich");
  const [title, setTitle] = useState("");
  const [folder, setFolder] = useState("/");
  const [content, setContent] = useState("");
  const [encrypted, setEncrypted] = useState(false);
  const [plainUnlocked, setPlainUnlocked] = useState(true);
  const [status, setStatus] = useState<"idle" | "dirty" | "saving" | "saved" | "offline">("idle");
  const [shareOpen, setShareOpen] = useState(false);
  const loadedNoteID = useRef(0);
  const lastSavedRef = useRef("");
  const pendingCleanSignatureRef = useRef("");
  const skipDirtyForLoadedNote = useRef(0);
  const savedSignaturesRef = useRef<Map<number, string>>(new Map());
  const snapshotsRef = useRef<Map<number, EditorSnapshot>>(new Map());
  const lastUnlockedSnapshotRef = useRef<EditorSnapshot | null>(null);
  const [assetURLMap, setAssetURLMap] = useState<Map<string, string>>(() => new Map());
  const assetURLMapRef = useRef<Map<string, string>>(new Map());
  const assetURLMapNoteIDRef = useRef(0);
  const encryptedAssetURLKey = useMemo(() => {
    if (!activeNote?.is_encrypted || !plainUnlocked || !securityUnlock || !content) return "";
    return encryptedAssetURLs(content).join("\n");
  }, [activeNote?.is_encrypted, content, plainUnlocked, securityUnlock]);

  useEffect(() => {
    const noteEncrypted = Boolean(activeNote?.is_encrypted);
    const noteID = activeNote?.id || 0;
    if (assetURLMapNoteIDRef.current !== noteID) {
      revokeAssetURLMap(assetURLMapRef.current);
      assetURLMapRef.current = new Map();
      assetURLMapNoteIDRef.current = noteID;
      setAssetURLMap(new Map());
    }
    const contentValue = version?.content || "";
    const unlockedContent = noteEncrypted && !looksEncrypted(contentValue);
    const titleValue = noteEncrypted && !unlockedContent ? "Encrypted note" : activeNote?.title || "";
    loadedNoteID.current = noteID;
    skipDirtyForLoadedNote.current = noteID;
    setTitle(titleValue);
    setFolder(activeNote?.folder_path || "/");
    setEncrypted(noteEncrypted);
    setPlainUnlocked(!noteEncrypted || unlockedContent);
    setContent(noteEncrypted && !unlockedContent ? "" : contentValue);
    const signature = JSON.stringify({ title: titleValue, folder: activeNote?.folder_path || "/", content: noteEncrypted && !unlockedContent ? "" : contentValue, encrypted: noteEncrypted });
    lastSavedRef.current = signature;
    pendingCleanSignatureRef.current = signature;
    if (activeNote?.id) savedSignaturesRef.current.set(activeNote.id, signature);
    setStatus("idle");
    setMode("rich");
  }, [activeNote?.id, activeNote?.folder_path, activeNote?.is_encrypted, activeNote?.title, version?.content]);

  useEffect(() => {
    return () => {
      revokeAssetURLMap(assetURLMapRef.current);
      assetURLMapRef.current = new Map();
    };
  }, []);

  useEffect(() => {
    if (!activeNote || !version || !activeNote.is_encrypted || plainUnlocked || !securityUnlock) return;
    const note = activeNote;
    const currentVersion = version;
    const unlock = securityUnlock;
    let cancelled = false;
    async function decryptEncryptedNote() {
      const nextTitle = looksEncrypted(note.title) ? await decryptText(note.title, unlock.privateKeyArmored, unlock.passphrase) : note.title;
      const nextContent = looksEncrypted(currentVersion.content) ? await decryptText(currentVersion.content, unlock.privateKeyArmored, unlock.passphrase) : currentVersion.content;
      if (cancelled) return;
      setTitle(nextTitle || "Untitled");
      setContent(nextContent || "");
      setPlainUnlocked(true);
      rememberDecryptedTitle(note.id, nextTitle || "Untitled");
      setNotes((items) => (items || []).map((item) => item.id === note.id ? { ...item, title: nextTitle || "Untitled" } : item));
      const signature = JSON.stringify({ title: nextTitle || "Untitled", folder: note.folder_path || "/", content: nextContent || "", encrypted: true });
      lastSavedRef.current = signature;
      pendingCleanSignatureRef.current = signature;
      savedSignaturesRef.current.set(note.id, signature);
    }
    void decryptEncryptedNote().catch((err) => addToast(messageFromError(err), "error"));
    return () => { cancelled = true; };
  }, [activeNote, addToast, plainUnlocked, rememberDecryptedTitle, securityUnlock, setNotes, version]);

  useEffect(() => {
    if (!activeNote?.is_encrypted || !plainUnlocked || !securityUnlock || !encryptedAssetURLKey) {
      setAssetURLMap((current) => {
        const next = pruneAssetURLMap(current, new Set());
        assetURLMapRef.current = next;
        return new Map(next);
      });
      return;
    }
    const urls = encryptedAssetURLKey.split("\n").filter(Boolean);
    if (urls.length === 0) {
      setAssetURLMap((current) => pruneAssetURLMap(current, new Set()));
      return;
    }
    const needed = new Set(urls);
    const unlock = securityUnlock;
    let cancelled = false;
    async function decryptAssets() {
      const next = pruneAssetURLMap(assetURLMapRef.current, needed);
      const created: string[] = [];
      for (const url of urls) {
        if (next.has(url)) continue;
        try {
          const res = await fetch(url, { headers: { Accept: "application/octet-stream" } });
          if (!res.ok) continue;
          const encryptedBytes = await res.arrayBuffer();
          const plain = await decryptBytes(encryptedBytes, unlock.privateKeyArmored, unlock.passphrase);
          const blob = new Blob([bytesToArrayBuffer(plain)], { type: contentTypeFromAssetURL(url) });
          const objectURL = URL.createObjectURL(blob);
          created.push(objectURL);
          next.set(url, objectURL);
        } catch {
          // Leave undecryptable asset links as their original authenticated URL.
        }
      }
      if (!cancelled) {
        assetURLMapRef.current = next;
        setAssetURLMap(new Map(next));
      }
      else created.forEach((value) => URL.revokeObjectURL(value));
    }
    void decryptAssets().catch((err) => addToast(messageFromError(err), "error"));
    return () => { cancelled = true; };
  }, [activeNote?.is_encrypted, addToast, encryptedAssetURLKey, plainUnlocked, securityUnlock]);

  const flushSnapshot = useCallback(async (noteID: number, cause: SaveCause = "auto") => {
    const snap = snapshotsRef.current.get(noteID);
    if (!snap) return;
    if (snap.encrypted && !snap.plainUnlocked) {
      if (cause === "manual") addToast("Unlock the app before editing or saving encrypted notes.", "error");
      return;
    }
    if (cause === "auto" && snap.signature === savedSignaturesRef.current.get(noteID)) return;
    let saveTitle = snap.title;
    let saveContent = snap.content;
    if (snap.encrypted) {
      const encryptionKey = snap.securityUnlock?.publicKeyArmored || snap.defaultKey?.public_key_armored || "";
      if (!snap.securityUnlock || !encryptionKey) {
        if (cause === "manual") addToast("Unlock the app before saving encrypted notes.", "error");
        return;
      }
      saveTitle = await encryptText(snap.title || "Untitled", encryptionKey);
      saveContent = await encryptText(snap.content || "", encryptionKey);
    }
    const edit = { note_id: snap.activeNote.id, base_version_id: snap.version.id, title: saveTitle, folder_path: snap.folder, content: saveContent, header_json: "{}", client_id: crypto.randomUUID(), is_encrypted: snap.encrypted, autosave: cause === "auto" };
    if (!navigator.onLine) {
      await queueEdit(edit);
      if (activeNote?.id === snap.activeNote.id) setStatus("offline");
      return;
    }
    if (activeNote?.id === snap.activeNote.id) setStatus("saving");
    const data = await api.saveNote(csrf, snap.activeNote.id, edit);
    if (snap.encrypted) rememberDecryptedTitle(data.note.id, snap.title || "Untitled");
    if (activeNote?.id === snap.activeNote.id) {
      setActiveNote(snap.encrypted ? { ...data.note, title: snap.title } : data.note);
      setVersion(snap.encrypted ? { ...data.version, content: snap.content } : data.version);
      if (window.location.pathname.startsWith("/notes/")) {
        window.history.replaceState({ note: data.note.slug }, "", noteURL(snap.encrypted ? { ...data.note, title: snap.title } : data.note));
      }
    }
    setNotes((items) => (items || []).map((item) => item.id === data.note.id ? { ...data.note, title: snap.encrypted ? snap.title : data.note.title, preview: snap.encrypted ? "" : snap.content.slice(0, 180) } : item));
    savedSignaturesRef.current.set(noteID, snap.signature);
    if (activeNote?.id === snap.activeNote.id) {
      lastSavedRef.current = snap.signature;
      setStatus("saved");
    }
    if (cause === "manual") addToast(data.conflict ? "Saved as a conflicted version." : "Version saved.");
  }, [activeNote?.id, addToast, csrf, rememberDecryptedTitle, setActiveNote, setNotes, setVersion]);

  useEffect(() => {
    if (!activeNote?.is_encrypted || securityUnlock || !plainUnlocked) return;
    const note = activeNote;
    let cancelled = false;
    const snap = lastUnlockedSnapshotRef.current;
    async function lockEncryptedNote() {
      if (snap?.activeNote.id === note.id) {
        snapshotsRef.current.set(note.id, snap);
        await flushSnapshot(note.id, "auto").catch((err) => {
          addToast(`Autosave queued: ${messageFromError(err)}`, "error");
        });
      }
      if (cancelled) return;
      setTitle("Encrypted note");
      setContent("");
      setPlainUnlocked(false);
      setMode("rich");
      setStatus("idle");
      if (window.location.pathname.startsWith("/notes/")) {
        window.history.replaceState({ note: note.slug }, "", `/notes/${note.slug || note.id}/encrypted-note`);
      }
      const signature = JSON.stringify({ title: "Encrypted note", folder: note.folder_path || "/", content: "", encrypted: true });
      lastSavedRef.current = signature;
      savedSignaturesRef.current.set(note.id, signature);
    }
    void lockEncryptedNote();
    return () => { cancelled = true; };
  }, [activeNote, addToast, flushSnapshot, plainUnlocked, securityUnlock]);

  const save = useCallback(async (cause: SaveCause) => {
    if (!activeNote) return;
    await flushSnapshot(activeNote.id, cause);
  }, [activeNote, flushSnapshot]);

  if (activeNote && version) {
    const signature = JSON.stringify({ title, folder, content, encrypted });
    const snapshot = { activeNote, version, title, folder, content, encrypted, plainUnlocked, securityUnlock, defaultKey, signature };
    snapshotsRef.current.set(activeNote.id, snapshot);
    if (encrypted && plainUnlocked && securityUnlock) lastUnlockedSnapshotRef.current = snapshot;
  }

  useEffect(() => {
    if (!activeNote || !version || loadedNoteID.current !== activeNote.id) return;
    const signature = JSON.stringify({ title, folder, content, encrypted });
    if (pendingCleanSignatureRef.current && signature === pendingCleanSignatureRef.current) {
      pendingCleanSignatureRef.current = "";
      lastSavedRef.current = signature;
      if (activeNote?.id) savedSignaturesRef.current.set(activeNote.id, signature);
      setStatus("idle");
      return;
    }
    if (signature === lastSavedRef.current) return;
    if (skipDirtyForLoadedNote.current === activeNote.id) {
      skipDirtyForLoadedNote.current = 0;
      return;
    }
    setStatus("dirty");
    const timer = window.setTimeout(() => {
      void save("auto").catch((err) => {
        setStatus("offline");
        if (!encrypted) void queueEdit({ note_id: activeNote.id, base_version_id: version.id, title, folder_path: folder, content, header_json: "{}", client_id: crypto.randomUUID(), is_encrypted: encrypted, autosave: true });
        addToast(`Autosave queued: ${messageFromError(err)}`, "error");
      });
    }, 1800);
    return () => window.clearTimeout(timer);
  }, [activeNote, addToast, content, encrypted, folder, save, title, version]);

  useEffect(() => {
    const noteID = activeNote?.id || 0;
    return () => {
      if (noteID) void flushSnapshot(noteID, "auto").catch(() => undefined);
    };
  }, [activeNote?.id, flushSnapshot]);

  const restoreAssetMarkdown = useCallback((markdown: string) => restoreAssetURLs(markdown, assetURLMapRef.current), []);

  useEffect(() => {
    const flushCurrent = () => {
      const noteID = activeNote?.id || 0;
      if (noteID) void flushSnapshot(noteID, "auto").catch(() => undefined);
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") flushCurrent();
    };
    window.addEventListener("pagehide", flushCurrent);
    window.addEventListener("beforeunload", flushCurrent);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("pagehide", flushCurrent);
      window.removeEventListener("beforeunload", flushCurrent);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [activeNote?.id, flushSnapshot]);

  async function uploadAssetURL(file: File) {
    if (!activeNote) return;
    if (encrypted) {
      const encryptionKey = securityUnlock?.publicKeyArmored || defaultKey?.public_key_armored || "";
      if (!securityUnlock || !encryptionKey) {
        openUnlock();
        throw new Error("Unlock the app before attaching files to encrypted notes.");
      }
      const encryptedData = await encryptBytes(await file.arrayBuffer(), encryptionKey);
      const encryptedFile = new File([bytesToArrayBuffer(encryptedData)], file.name, { type: "application/octet-stream" });
      const data = await api.uploadAsset(csrf, encryptedFile, activeNote.id, true, file.type || "application/octet-stream");
      const objectURL = await fileDataURL(file);
      const next = new Map(assetURLMapRef.current);
      const previous = next.get(data.url);
      if (previous?.startsWith("blob:")) URL.revokeObjectURL(previous);
      next.set(data.url, objectURL);
      assetURLMapRef.current = next;
      setAssetURLMap(new Map(next));
      return objectURL;
    }
    const data = await api.uploadAsset(csrf, file, activeNote.id);
    return data.url;
  }

  async function upload(file: File) {
    const url = await uploadAssetURL(file);
    if (!url) return;
    const markdown = file.type.startsWith("image/") ? `![${file.name}](${url})` : `[${file.name}](${url})`;
    setContent((current) => insertAtEnd(current, markdown));
  }

  async function trashCurrent() {
    if (!activeNote) return;
    const sharedOnly = activeNote.owner_user_id !== user.id;
    const message = sharedOnly
      ? "Remove this shared note from your account? The original note will not be deleted."
      : "Move this note to Trash?";
    if (!window.confirm(message)) return;
    const data = await api.trashNote(csrf, activeNote.id);
    setActiveNote(data.note);
    setVersion(data.version);
    setNotes((items) => (items || []).filter((item) => item.id !== activeNote.id));
    addToast("Note moved to trash.");
  }

  async function wipeCurrent() {
    if (!activeNote) return;
    const sharedOnly = activeNote.owner_user_id !== user.id;
    const message = sharedOnly
      ? "Permanently remove this shared note for yourself? The original note will remain for its owner."
      : "Permanently delete this note? This cannot be undone.";
    if (!window.confirm(message)) return;
    await api.wipeNote(csrf, activeNote.id);
    setActiveNote(null);
    setVersion(null);
    setNotes((items) => (items || []).filter((item) => item.id !== activeNote.id));
    addToast("Note permanently deleted.");
  }

  async function restoreCurrent() {
    if (!activeNote) return;
    const data = await api.untrashNote(csrf, activeNote.id);
    setActiveNote(data.note);
    setVersion(data.version);
    await reloadNotes();
    addToast("Note restored.");
  }

  async function toggleSecurity() {
    if (!activeNote) return;
    if (encrypted) {
      if (plainUnlocked) {
        setContent("");
        setTitle("Encrypted note");
        setPlainUnlocked(false);
        setStatus("idle");
        lastSavedRef.current = JSON.stringify({ title: "Encrypted note", folder, content: "", encrypted: true });
        addToast("Encrypted note locked in this browser.");
        return;
      }
      if (!securityUnlock) {
        openUnlock();
        return;
      }
      const data = await api.note(activeNote.id);
      const plainTitle = looksEncrypted(data.note.title) ? await decryptText(data.note.title, securityUnlock.privateKeyArmored, securityUnlock.passphrase) : data.note.title;
      const plain = looksEncrypted(data.version.content) ? await decryptText(data.version.content, securityUnlock.privateKeyArmored, securityUnlock.passphrase) : data.version.content;
      setActiveNote({ ...data.note, title: plainTitle || "Untitled" });
      setVersion({ ...data.version, content: plain || "" });
      setTitle(plainTitle || "Untitled");
      setContent(plain || "");
      setPlainUnlocked(true);
      lastSavedRef.current = JSON.stringify({ title: plainTitle || "Untitled", folder: data.note.folder_path || "/", content: plain || "", encrypted: true });
      addToast("Encrypted note unlocked in this browser.");
      return;
    }
    if (!defaultKey) {
      addToast("Create or import a default PGP key in Settings first.", "error");
      return;
    }
    if (!securityUnlock) {
      openUnlock();
      return;
    }
    setEncrypted(true);
    setPlainUnlocked(true);
    addToast("Note is now encrypted. Autosave will store encrypted title and body.");
  }

  if (!activeNote || !version) return <div className="empty-state"><h1>No note selected</h1><p className="muted">Create a note or choose one from the folder browser.</p></div>;
  const richContent = encrypted && plainUnlocked && assetURLMap.size > 0 ? replaceAssetURLs(content, assetURLMap) : content;

  return (
    <section className="editor single-editor">
      <div className="editor-head">
        <div className="title-stack">
          <input className="title-input" value={title} readOnly={encrypted && !plainUnlocked} onChange={(event) => setTitle(event.target.value)} />
          <div className="note-meta-row">
            <span className="folder-chip"><FolderIcon />{folder || "/"}{activeNote.is_shared ? <ShareNetworkIcon className="shared-badge" /> : null}</span>
            {encrypted ? <span className="pgp-status-chip"><LockIcon />PGP encrypted {plainUnlocked ? "unlocked" : "locked"}</span> : null}
            {status !== "idle" ? <span className={`save-state ${status}`}>{saveLabel(status)}</span> : null}
          </div>
        </div>
        <div className="editor-actions">
          <div className="mode-toggle" role="group" aria-label="Editor mode">
            <button type="button" className={mode === "rich" ? "active" : ""} title="Rich editor" aria-label="Rich editor" onClick={() => setMode("rich")}><PencilSimpleIcon /></button>
            <button type="button" className={mode === "raw" ? "active" : ""} title="Raw markdown" aria-label="Raw markdown" onClick={() => setMode("raw")}><CodeIcon /></button>
          </div>
          <button type="button" className={`icon-only secondary ${mode === "history" ? "active" : ""}`} title="Version history" aria-label="Version history" onClick={() => setMode("history")}><ClockCounterClockwiseIcon /></button>
          <button type="button" className={`icon-only secondary ${activeNote.is_shared ? "shared-action" : ""}`} title="Share note" aria-label="Share note" onClick={() => {
            if (activeNote.is_encrypted) {
              addToast("Encrypted notes cannot be shared.", "error");
              return;
            }
            setShareOpen(true);
          }}><ShareNetworkIcon /></button>
          {isNoteTrashed(activeNote) ? <>
            <button type="button" className="icon-only secondary" title="Restore" aria-label="Restore" onClick={() => void restoreCurrent().catch((err) => addToast(messageFromError(err), "error"))}><ClockCounterClockwiseIcon /></button>
            <button type="button" className="icon-only secondary" title="Delete forever" aria-label="Delete forever" onClick={() => void wipeCurrent().catch((err) => addToast(messageFromError(err), "error"))}><TrashIcon /></button>
          </> : <button type="button" className="icon-only secondary" title="Move to trash" aria-label="Move to trash" onClick={() => void trashCurrent().catch((err) => addToast(messageFromError(err), "error"))}><TrashIcon /></button>}
          <button type="button" className="icon-only" title="Save version" aria-label="Save version" onClick={() => void save("manual").catch((err) => addToast(messageFromError(err), "error"))}><FloppyDiskIcon /></button>
        </div>
      </div>
      {encrypted && !plainUnlocked ? <LockedNoteView openUnlock={openUnlock} /> : null}
      {plainUnlocked && mode === "rich" ? <RichMarkdownEditor key={activeNote.id} content={richContent} restoreAssetMarkdown={restoreAssetMarkdown} setContent={setContent} uploadAssetURL={uploadAssetURL} addToast={addToast} /> : null}
      {plainUnlocked && mode === "raw" ? <RawMarkdownEditor content={content} setContent={setContent} upload={upload} addToast={addToast} /> : null}
      {plainUnlocked && mode === "history" ? <History noteID={activeNote.id} currentUser={user} encrypted={activeNote.is_encrypted} securityUnlock={securityUnlock} openUnlock={openUnlock} csrf={csrf} openNote={(id) => api.note(id).then((data) => { setActiveNote(data.note); setVersion(data.version); setShares(data.shares || []); })} addToast={addToast} /> : null}
      {shareOpen && !activeNote.is_encrypted ? <ShareDialog csrf={csrf} noteID={activeNote.id} shares={shares} setShares={setShares} onClose={() => setShareOpen(false)} addToast={addToast} /> : null}
    </section>
  );
}

function RichMarkdownEditor({ content, restoreAssetMarkdown, setContent, uploadAssetURL, addToast }: { content: string; restoreAssetMarkdown: (markdown: string) => string; setContent: (value: string | ((current: string) => string)) => void; uploadAssetURL: (file: File) => Promise<string | undefined>; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const editorRef = useRef<MDXEditorMethods>(null);
  const lastMarkdown = useRef(content);
  const plugins = useMemo(() => [
    headingsPlugin(),
    listsPlugin(),
    quotePlugin(),
    thematicBreakPlugin(),
    linkPlugin(),
    linkDialogPlugin(),
    tablePlugin(),
    imagePlugin({
      imageUploadHandler: async (file) => {
        const url = await uploadAssetURL(file);
        if (!url) throw new Error("Upload failed");
        return url;
      }
    }),
    markdownShortcutPlugin(),
    toolbarPlugin({
      toolbarContents: () => (
        <>
          <UndoRedo />
          <Separator />
          <BlockTypeSelect />
          <BoldItalicUnderlineToggles />
          <CodeToggle />
          <Separator />
          <ListsToggle />
          <CreateLink />
          <InsertImage />
          <InsertTable />
          <InsertThematicBreak />
        </>
      )
    })
  ], [uploadAssetURL]);

  useEffect(() => {
    if (content === lastMarkdown.current) return;
    lastMarkdown.current = content;
    editorRef.current?.setMarkdown(content);
  }, [content]);

  async function insertFiles(files: File[]) {
    for (const file of files) {
      const url = await uploadAssetURL(file);
      if (!url) continue;
      const markdown = file.type.startsWith("image/") ? `![${file.name}](${url})` : `[${file.name}](${url})`;
      editorRef.current?.insertMarkdown(`\n\n${markdown}\n`);
    }
  }

  return (
    <div className="rich-editor" onDragOverCapture={(event) => event.preventDefault()} onDropCapture={(event) => {
      event.preventDefault();
      event.stopPropagation();
      const files = Array.from(event.dataTransfer.files || []);
      void insertFiles(files).catch((err) => addToast(messageFromError(err), "error"));
    }}>
      <MDXEditor
        ref={editorRef}
        className="mdx-rich-editor light-theme"
        contentEditableClassName="mdx-rich-content"
        markdown={content}
        plugins={plugins}
        placeholder="Start writing..."
        spellCheck
        onChange={(markdown, initialNormalize) => {
          lastMarkdown.current = markdown;
          if (!initialNormalize) setContent(restoreAssetMarkdown(markdown));
        }}
        onError={(payload) => addToast(payload.error, "error")}
      />
    </div>
  );
}

function LockedNoteView({ openUnlock }: { openUnlock: () => void }) {
  return (
    <div className="locked-note-view">
      <LockIcon />
      <h2>Encrypted note locked</h2>
      <p className="muted">Unlock the app to decrypt and edit this note. Ciphertext is hidden from the editor.</p>
      <button type="button" onClick={openUnlock}><LockOpenIcon />Unlock app</button>
    </div>
  );
}

function RawMarkdownEditor({ content, setContent, upload, addToast }: { content: string; setContent: (value: string | ((current: string) => string)) => void; upload: (file: File) => Promise<void>; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  return (
    <div className="raw-editor">
      <textarea className="markdown-editor" value={content} onChange={(event) => setContent(event.target.value)} onDrop={(event) => {
        event.preventDefault();
        Array.from(event.dataTransfer.files || []).forEach((file) => void upload(file).catch((err) => addToast(messageFromError(err), "error")));
      }} />
    </div>
  );
}

function FolderBrowser({ folders, notes, activeNoteID, selectedFolder, expandedFolders, setExpandedFolders, setSelectedFolder, openNote, createFolder, moveNotesToFolder, moveFolderToFolder, selectedNoteIDs, securityUnlocked, dateFormat, requestImport }: {
  folders: FolderRecord[];
  notes: NoteSummary[];
  activeNoteID: number;
  selectedFolder: string;
  expandedFolders: Set<string>;
  setExpandedFolders: React.Dispatch<React.SetStateAction<Set<string>>>;
  setSelectedFolder: (path: string) => void;
  openNote: (id: number | string) => Promise<void>;
  createFolder: (parent: string, name: string) => Promise<void>;
  moveNotesToFolder: (noteIDs: number[], targetFolder: string) => Promise<void>;
  moveFolderToFolder: (source: string, targetParent: string) => Promise<void>;
  selectedNoteIDs: Set<number>;
  securityUnlocked: boolean;
  dateFormat: string;
  requestImport: (files: File[], folderPath: string) => void;
}) {
  const [creatingUnder, setCreatingUnder] = useState<string | null>(null);
  const [folderName, setFolderName] = useState("");
  const [dragTarget, setDragTarget] = useState("");
  const root = useMemo(() => buildFolderTree(folders, notes), [folders, notes]);

  function toggle(path: string) {
    setExpandedFolders((current) => {
      const next = new Set(current);
      next.has(path) ? next.delete(path) : next.add(path);
      next.add("/");
      return next;
    });
  }

  async function submitFolder(parent: string) {
    const name = folderName.trim();
    if (!name) return;
    await createFolder(parent, name);
    setFolderName("");
    setCreatingUnder(null);
  }

  function renderNode(node: FolderNode, depth: number) {
    const expanded = expandedFolders.has(node.path);
    const hasChildren = node.children.length > 0 || node.notes.length > 0;
    const selected = selectedFolder === node.path || (node.path === "/" && selectedFolder === "");
    return (
      <div className="folder-node" key={node.path}>
        <div
          className={`folder-row ${selected ? "active" : ""} ${dragTarget === node.path ? "drag-over" : ""}`}
          style={{ "--depth": depth } as React.CSSProperties}
          draggable={node.path !== "/"}
          onDragStart={(event) => {
            if (node.path === "/") return;
            event.dataTransfer.effectAllowed = "move";
            event.dataTransfer.setData("text/folder-path", node.path);
          }}
          onDragOver={(event) => { event.preventDefault(); setDragTarget(node.path); }}
          onDragLeave={() => setDragTarget((current) => current === node.path ? "" : current)}
          onDrop={(event) => {
            event.preventDefault();
            setDragTarget("");
            const files = Array.from(event.dataTransfer.files || []).filter((file) => /\.(md|zip)$/i.test(file.name));
            if (files.length > 0) {
              requestImport(files, node.path);
              return;
            }
            const sourceFolder = event.dataTransfer.getData("text/folder-path");
            if (sourceFolder) {
              void moveFolderToFolder(sourceFolder, node.path);
              return;
            }
            const noteIDs = (event.dataTransfer.getData("text/note-ids") || "")
              .split(",")
              .map((value) => Number(value))
              .filter(Boolean);
            const noteID = Number(event.dataTransfer.getData("text/note-id"));
            const ids = noteIDs.length > 0 ? noteIDs : noteID ? [noteID] : [];
            if (ids.length > 0) void moveNotesToFolder(ids, node.path);
          }}
        >
          <button type="button" className="folder-toggle ghost" onClick={() => toggle(node.path)} title={expanded ? "Collapse folder" : "Expand folder"} disabled={!hasChildren}>
            {expanded ? <CaretDownIcon /> : <CaretRightIcon />}
          </button>
          <button type="button" className="folder-label ghost" onClick={() => { setSelectedFolder(node.path); setExpandedFolders((current) => expandAncestors(current, node.path)); }}>
            <FolderIcon />{node.path === "/" ? "All Notes" : node.name}
            <span className="folder-count">{node.noteCount}</span>
          </button>
          <button type="button" className="folder-add ghost" title="New folder" onClick={() => { setCreatingUnder(node.path); setFolderName(""); setExpandedFolders((current) => expandAncestors(current, node.path)); }}>
            <FolderPlusIcon />
          </button>
        </div>
        {creatingUnder === node.path ? (
          <form className="folder-create-row" style={{ "--depth": depth + 1 } as React.CSSProperties} onSubmit={(event) => { event.preventDefault(); void submitFolder(node.path); }}>
            <input value={folderName} autoFocus placeholder="Folder name" onChange={(event) => setFolderName(event.target.value)} />
            <button type="submit" className="icon-only folder-create-action" title="Create folder" aria-label="Create folder"><CheckIcon /></button>
            <button type="button" className="icon-only ghost folder-create-action" title="Cancel" aria-label="Cancel" onClick={() => setCreatingUnder(null)}>×</button>
          </form>
        ) : null}
        {expanded ? (
          <div className="folder-children">
            {node.children.map((child) => renderNode(child, depth + 1))}
            {node.notes.map((note) => (
              <div
                key={note.id}
                className={`sidebar-note-row ${activeNoteID === note.id ? "active" : ""}`}
                style={{ "--depth": depth + 1 } as React.CSSProperties}
                onClick={(event) => {
                  if ((event.target as HTMLElement).closest(".note-drag-handle")) return;
                  void openNote(note.id);
                }}
              >
                <span
                  className="note-drag-handle"
                  title="Drag note"
                draggable
                onDragStart={(event) => {
                  event.dataTransfer.effectAllowed = "move";
                  const ids = selectedNoteIDs.has(note.id) ? Array.from(selectedNoteIDs) : [note.id];
                  event.dataTransfer.setData("text/note-ids", ids.join(","));
                  event.dataTransfer.setData("text/note-id", String(note.id));
                }}
                >
                  <DotsSixVerticalIcon />
                </span>
                <button
                  type="button"
                  className="sidebar-note-button"
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    void openNote(note.id);
                  }}
                >
                  <strong><FileTextIcon className="document-note-icon" />{note.is_encrypted ? <LockIcon className="encrypted-note-icon" /> : null}{note.is_shared ? <ShareNetworkIcon className="shared-badge" /> : null}<NoteTitle note={note} securityUnlocked={securityUnlocked} /></strong>
                  <span>{formatSidebarDate(note.updated_at, dateFormat)}</span>
                </button>
              </div>
            ))}
          </div>
        ) : null}
      </div>
    );
  }

  return <div className="folder-tree">{renderNode(root, 0)}</div>;
}

function PGPUnlockDialog({ keys, onClose, onUnlocked, addToast }: { keys: EncryptionKey[]; onClose: () => void; onUnlocked: (state: SecurityUnlock) => void; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const [keyID, setKeyID] = useState((keys.find((key) => key.is_default) || keys[0])?.id || 0);
  const [passphrase, setPassphrase] = useState("");
  const [durationMinutes, setDurationMinutes] = useState(30);
  const [busy, setBusy] = useState(false);
  const selected = keys.find((key) => key.id === keyID) || keys.find((key) => key.is_default) || keys[0];

  async function unlock() {
    if (!selected) {
      addToast("Create or import a PGP key first.", "error");
      return;
    }
    setBusy(true);
    try {
      let armored = "";
      if (selected.storage_mode === "browser") armored = await loadBrowserPGPKey(selected.id);
      if (!armored && selected.encrypted_private_key) armored = selected.encrypted_private_key;
      if (!armored || !passphrase) {
        addToast("This key has no saved private key in this browser or on the server.", "error");
        return;
      }
      await verifyPrivateKey(armored, passphrase);
      onUnlocked({
        keyID: selected.id,
        label: selected.label,
        fingerprint: selected.fingerprint,
        publicKeyArmored: selected.public_key_armored,
        privateKeyArmored: armored,
        passphrase,
        unlockedUntil: Date.now() + durationMinutes * 60_000
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <form className="security-dialog unlock-dialog" role="dialog" aria-label="Unlock PGP key" onSubmit={(event) => { event.preventDefault(); void unlock().catch((err) => addToast(messageFromError(err), "error")); }} onClick={(event) => event.stopPropagation()}>
        <div className="modal-head">
          <div><h2>Unlock app</h2><p className="muted">Unlock a configured PGP key for this browser session.</p></div>
          <button type="button" className="ghost" onClick={onClose}>Close</button>
        </div>
        <label>Key<select value={keyID} onChange={(event) => setKeyID(Number(event.target.value))}>{keys.map((key) => <option key={key.id} value={key.id}>{key.is_default ? "Default - " : ""}{key.label || shortFingerprint(key.fingerprint)}</option>)}</select></label>
        <label>Passphrase<input type="password" value={passphrase} onChange={(event) => setPassphrase(event.target.value)} autoComplete="current-password" /></label>
        <label>Keep unlocked<select value={durationMinutes} onChange={(event) => setDurationMinutes(Number(event.target.value))}><option value={15}>15 minutes</option><option value={30}>30 minutes</option><option value={60}>1 hour</option><option value={240}>4 hours</option></select></label>
        <div className="notice subtle">Browser-only keys unlock only where you saved that private key. To add a private key, use Settings.</div>
        <div className="modal-actions"><button type="button" className="secondary" onClick={onClose}>Cancel</button><button type="submit" disabled={busy || keys.length === 0 || !passphrase}>{busy ? "Unlocking..." : "Unlock"}</button></div>
      </form>
    </div>
  );
}

function NoteListView({ csrf, title, subtitle, results, setResults, selected, setSelected, openNote, securityUnlocked, addToast, highlight = "", action = null, page, hasMore, onPageChange }: { csrf: string; title: string; subtitle: string; results: NoteSummary[]; setResults: React.Dispatch<React.SetStateAction<NoteSummary[]>>; selected: Set<number>; setSelected: React.Dispatch<React.SetStateAction<Set<number>>>; openNote: (id: number | string) => Promise<void>; securityUnlocked: boolean; addToast: (message: string, kind?: Toast["kind"]) => number; highlight?: string; action?: React.ReactNode; page: number; hasMore: boolean; onPageChange: (page: number) => Promise<void> }) {
  const [selectionAnchor, setSelectionAnchor] = useState<number | null>(null);
  const allSelected = results.length > 0 && selected.size === results.length;
  function toggleSelected(id: number, checked: boolean, shiftKey = false) {
    setSelected((current) => {
      const next = new Set(current);
      if (shiftKey && selectionAnchor !== null) {
        const from = results.findIndex((note) => note.id === selectionAnchor);
        const to = results.findIndex((note) => note.id === id);
        if (from >= 0 && to >= 0) {
          const [start, end] = from < to ? [from, to] : [to, from];
          for (const note of results.slice(start, end + 1)) {
            checked ? next.add(note.id) : next.delete(note.id);
          }
          return next;
        }
      }
      checked ? next.add(id) : next.delete(id);
      return next;
    });
    setSelectionAnchor(id);
  }
  async function setStar(note: NoteSummary, starred: boolean) {
    const data = await api.starNote(csrf, note.id, starred);
    setResults((items) => items.map((item) => item.id === note.id ? { ...item, is_starred: data.note.is_starred } : item));
  }
  async function setSelectedStarred(starred: boolean) {
    const ids = Array.from(selected);
    for (const id of ids) {
      await api.starNote(csrf, id, starred);
    }
    setResults((items) => items.map((item) => selected.has(item.id) ? { ...item, is_starred: starred } : item));
    addToast(starred ? "Selected notes starred." : "Selected notes unstarred.");
  }
  return (
    <section className="search-page">
      <div className="search-page-head">
        <h1>{title}</h1>
        <div className="search-page-actions">
          <span>{subtitle}</span>
          <Pager page={page} hasMore={hasMore} onPageChange={(nextPage) => void onPageChange(nextPage).catch((err) => addToast(messageFromError(err), "error"))} />
          {action}
        </div>
      </div>
      <div className="selection-bar">
        <label className="select-all"><input type="checkbox" checked={allSelected} onChange={(event) => {
          setSelected(event.target.checked ? new Set(results.map((note) => note.id)) : new Set());
          setSelectionAnchor(null);
        }} />{selected.size ? `${selected.size} selected` : "Select"}</label>
        {selected.size > 0 ? <>
          <button type="button" className="icon-only secondary" title="Star selected" aria-label="Star selected" onClick={() => void setSelectedStarred(true).catch((err) => addToast(messageFromError(err), "error"))}><StarIcon /></button>
          <button type="button" className="icon-only secondary" title="Unstar selected" aria-label="Unstar selected" onClick={() => void setSelectedStarred(false).catch((err) => addToast(messageFromError(err), "error"))}><StarIcon weight="regular" /></button>
        </> : null}
      </div>
      <div className="search-results">
        {results.map((note) => <button type="button" className="search-result" key={note.id} draggable onDragStart={(event) => {
          const ids = selected.has(note.id) ? Array.from(selected) : [note.id];
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData("text/note-ids", ids.join(","));
          event.dataTransfer.setData("text/note-id", String(note.id));
        }} onClick={() => void openNote(note.id)}>
          <span className="result-select" onClick={(event) => event.stopPropagation()}><input type="checkbox" checked={selected.has(note.id)} onClick={(event) => toggleSelected(note.id, event.currentTarget.checked, event.shiftKey)} onChange={() => undefined} /></span>
          <span className="result-star" onClick={(event) => event.stopPropagation()}><button type="button" className={`icon-only ghost ${note.is_starred ? "starred" : ""}`} title={note.is_starred ? "Unstar note" : "Star note"} aria-label={note.is_starred ? "Unstar note" : "Star note"} onClick={() => void setStar(note, !note.is_starred).catch((err) => addToast(messageFromError(err), "error"))}><StarIcon weight={note.is_starred ? "fill" : "regular"} /></button></span>
          <div className="result-title-row">
            <strong>{note.is_shared ? <ShareNetworkIcon className="shared-badge" /> : null}<NoteTitle note={note} securityUnlocked={securityUnlocked} /></strong>
            <time>{formatDateTime(note.updated_at)}</time>
          </div>
          <div className="result-meta"><span className="result-path"><FolderIcon />{note.folder_path || "/"}</span>{note.is_encrypted ? <span className="pgp-note-status">PGP encrypted</span> : null}<span>Created {formatDateTime(note.created_at)}</span></div>
          <p>{note.is_encrypted ? <span className="encrypted-preview">PGP encrypted <span className="blind-text">•••• •••• •••• ••••</span></span> : highlightPreview(previewText(note.preview) || "No preview", highlight)}</p>
        </button>)}
      </div>
    </section>
  );
}

function Pager({ page, hasMore, onPageChange }: { page: number; hasMore: boolean; onPageChange: (page: number) => void }) {
  return (
    <nav className="pager" aria-label="Pagination">
      <button type="button" className="secondary" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>Previous</button>
      <span>Page {page}</span>
      <button type="button" className="secondary" disabled={!hasMore} onClick={() => onPageChange(page + 1)}>Next</button>
    </nav>
  );
}

function History({ noteID, currentUser, encrypted, securityUnlock, openUnlock, csrf, openNote, addToast }: { noteID: number; currentUser: User; encrypted: boolean; securityUnlock: SecurityUnlock | null; openUnlock: () => void; csrf: string; openNote: (id: number) => Promise<void>; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const [versions, setVersions] = useState<NoteVersion[]>([]);
  const [compareIDs, setCompareIDs] = useState<number[]>([]);
  const [diffRows, setDiffRows] = useState<DiffRow[]>([]);
  const [diffOpen, setDiffOpen] = useState(false);
  const [diffBusy, setDiffBusy] = useState(false);
  useEffect(() => { void api.versions(noteID).then((data) => setVersions(data.versions || [])); }, [noteID]);
  const selectedVersions = compareIDs
    .map((id) => versions.find((version) => version.id === id))
    .filter((version): version is NoteVersion => Boolean(version))
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
  function toggleCompare(versionID: number) {
    setCompareIDs((current) => {
      if (current.includes(versionID)) return current.filter((id) => id !== versionID);
      return [...current.slice(-1), versionID];
    });
    setDiffOpen(false);
  }
  async function openDiff() {
    if (selectedVersions.length !== 2) return;
    if (encrypted && !securityUnlock) {
      addToast("Unlock the app before diffing encrypted versions.", "error");
      openUnlock();
      return;
    }
    setDiffBusy(true);
    try {
      const before = await versionDiffContent(selectedVersions[0], encrypted, securityUnlock);
      const after = await versionDiffContent(selectedVersions[1], encrypted, securityUnlock);
      setDiffRows(diffLines(before, after));
      setDiffOpen(true);
    } finally {
      setDiffBusy(false);
    }
  }
  return (
    <div className="panel history-panel">
      <div className="history-head">
        <h2>Version history</h2>
        <button type="button" className="secondary" disabled={compareIDs.length !== 2 || diffBusy} onClick={() => void openDiff().catch((err) => addToast(messageFromError(err), "error"))}><CodeIcon />{diffBusy ? "Diffing..." : "Diff"}</button>
      </div>
      {versions.map((v) => (
        <div className="version-row history-version-row" key={v.id}>
          <label className="history-compare"><input type="checkbox" checked={compareIDs.includes(v.id)} onChange={() => toggleCompare(v.id)} />Compare</label>
          <span>{new Date(v.created_at).toLocaleString()} · {versionAuthor(v, currentUser)} {v.conflicted ? "(conflict)" : ""}</span>
          <button type="button" className="secondary" onClick={() => api.restore(csrf, noteID, v.id).then(() => openNote(noteID)).then(() => addToast("Version restored.")).catch((err) => addToast(messageFromError(err), "error"))}>Restore</button>
        </div>
      ))}
      {diffOpen && selectedVersions.length === 2 ? (
        <div className="version-diff">
          <div className="diff-head">
            <strong>{versionLabel(selectedVersions[0], currentUser)}</strong>
            <span>to</span>
            <strong>{versionLabel(selectedVersions[1], currentUser)}</strong>
          </div>
          <div className="diff-body">
            {diffRows.map((row, index) => <div key={`${index}-${row.kind}`} className={`diff-line ${row.kind}`}><span>{diffPrefix(row.kind)}</span><code>{row.text || " "}</code></div>)}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function ShareDialog({ csrf, noteID, shares, setShares, onClose, addToast }: { csrf: string; noteID: number; shares: Share[]; setShares: (shares: Share[]) => void; onClose: () => void; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const [users, setUsers] = useState<User[]>([]);
  const [selectedUserID, setSelectedUserID] = useState(0);
  const [permission, setPermission] = useState<"read" | "write">("read");

  useEffect(() => {
    void api.users().then((data) => {
      const list = data.users || [];
      setUsers(list);
      setSelectedUserID((current) => current || list[0]?.id || 0);
    }).catch((err) => addToast(messageFromError(err), "error"));
  }, [addToast]);

  const selectedUser = users.find((user) => user.id === selectedUserID) || null;

  function submitShare(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedUser) return;
    void api.share(csrf, noteID, { email: selectedUser.email, permission }).then(() => api.note(noteID)).then((data) => { setShares(data.shares || []); addToast("Share saved."); }).catch((err) => addToast(messageFromError(err), "error"));
  }

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="security-dialog share-dialog" role="dialog" aria-label="Share note" onClick={(event) => event.stopPropagation()}>
        <div className="modal-head">
          <h2>Share note</h2>
          <button type="button" className="ghost" onClick={onClose}>Close</button>
        </div>
        {users.length === 0 ? (
          <div className="notice subtle">There are no other users on this instance yet.</div>
        ) : (
          <form className="share-form" onSubmit={submitShare}>
            <label>
              User
              <select value={selectedUserID} onChange={(event) => setSelectedUserID(Number(event.target.value))}>
                {users.map((user) => (
                  <option key={user.id} value={user.id}>
                    {user.name || user.email}{shares.some((share) => share.shared_user_id === user.id) ? " (shared)" : ""}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Permission
              <select value={permission} onChange={(event) => setPermission(event.target.value as "read" | "write")}>
                <option value="read">Read</option>
                <option value="write">Write</option>
              </select>
            </label>
            <button type="submit"><ShareNetworkIcon />Share</button>
          </form>
        )}
        {shares.length ? (
          <div className="share-current-list">
            {shares.map((share) => (
              <div className="version-row" key={share.shared_user_id}>
                <span>{share.name || share.email}<small>{share.email}</small></span>
                <strong>{share.permission}</strong>
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function SettingsView({ csrf, user, updateUser, templates, initialTemplateID, setTemplates, keys, setKeys, addToast }: { csrf: string; user: User; updateUser: (user: User) => void; templates: Template[]; initialTemplateID: number; setTemplates: (templates: Template[]) => void; keys: EncryptionKey[]; setKeys: (keys: EncryptionKey[] | ((current: EncryptionKey[]) => EncryptionKey[])) => void; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const [template, setTemplate] = useState<Partial<Template>>({ name: "", title_template: "Untitled", folder_template: "", body_template: "", create_once: false });
  const [templateSaving, setTemplateSaving] = useState(false);
  const [dateFormat, setDateFormat] = useState(user.date_format || "ymd_slash");
  const [profileSaving, setProfileSaving] = useState(false);
  const [storage, setStorage] = useState<"browser" | "server">("browser");
  const [passphrase, setPassphrase] = useState("");
  const [confirm, setConfirm] = useState("");
  const [importArmored, setImportArmored] = useState("");
  const [keyLabel, setKeyLabel] = useState("Cairnfield OpenPGP key");
  const [keyModal, setKeyModal] = useState<"import" | "generate" | null>(null);
  const [backups, setBackups] = useState<BackupExport[]>([]);
  const [backupBusy, setBackupBusy] = useState(false);
  const issues = passphraseIssues(passphrase, [user.name, user.email, user.email.split("@")[0] || "", user.email.split("@")[1] || ""]);

  useEffect(() => {
    setDateFormat(user.date_format || "ymd_slash");
  }, [user.date_format]);

  useEffect(() => {
    if (initialTemplateID === 0) {
      setTemplate({ name: "", title_template: "Untitled", folder_template: "", body_template: "", create_once: false });
      return;
    }
    const selected = templates.find((item) => item.id === initialTemplateID);
    if (selected) setTemplate(selected);
  }, [initialTemplateID, templates]);

  function resetTemplate() {
    setTemplate({ name: "", title_template: "Untitled", folder_template: "", body_template: "", is_default: templates.length === 0, create_once: false });
  }

  async function saveTemplate() {
    setTemplateSaving(true);
    try {
      const body = {
        ...template,
        name: template.name || "New template",
        title_template: template.title_template || "Untitled",
        folder_template: template.folder_template || "",
        body_template: template.body_template || "",
        create_once: Boolean(template.create_once)
      };
      const savedTemplate = await api.saveTemplate(csrf, body);
      const data = await api.templates();
      setTemplates(data.templates || []);
      const saved = (data.templates || []).find((item) => item.id === savedTemplate.template.id) || savedTemplate.template;
      setTemplate(saved || { name: "", title_template: "Untitled", folder_template: "", body_template: "", create_once: false });
      addToast("Template saved.");
    } finally {
      setTemplateSaving(false);
    }
  }

  async function deleteTemplate() {
    if (!template.id) return;
    if (!window.confirm(`Delete the "${template.name || "template"}" template?`)) return;
    await api.deleteTemplate(csrf, template.id);
    const data = await api.templates();
    setTemplates(data.templates || []);
    setTemplate((data.templates || [])[0] || { name: "", title_template: "Untitled", folder_template: "", body_template: "", is_default: true, create_once: false });
    addToast("Template deleted.");
  }

  async function saveProfile() {
    setProfileSaving(true);
    try {
      const data = await api.updateProfile(csrf, { date_format: dateFormat });
      updateUser(data.user);
      addToast("Profile saved.");
    } finally {
      setProfileSaving(false);
    }
  }

  const loadBackups = useCallback(async () => {
    const data = await api.backups();
    setBackups(data.backups || []);
  }, []);

  useEffect(() => {
    void loadBackups().catch((err) => addToast(messageFromError(err), "error"));
  }, [addToast, loadBackups]);

  useEffect(() => {
    if (!backups.some((backup) => backup.status === "running")) return;
    const timer = window.setTimeout(() => void loadBackups().catch((err) => addToast(messageFromError(err), "error")), 2500);
    return () => window.clearTimeout(timer);
  }, [addToast, backups, loadBackups]);

  async function saveGeneratedKey() {
    if (issues.length > 0) return addToast(issues[0], "error");
    if (passphrase !== confirm) return addToast("Passphrases do not match.", "error");
    const generated = await generateKey(user.name, user.email, passphrase);
    const saved = await api.saveKey(csrf, {
      label: keyLabel || "Cairnfield OpenPGP key",
      fingerprint: generated.fingerprint,
      public_key_armored: generated.publicKeyArmored,
      encrypted_private_key: storage === "server" ? generated.privateKeyArmored : "",
      storage_mode: storage,
      is_default: keys.length === 0
    });
    if (storage === "browser") {
      await saveBrowserPGPKey(saved.key.id, generated.privateKeyArmored);
      const verified = await loadBrowserPGPKey(saved.key.id);
      if (!verified) throw new Error("Generated key was created but could not be saved in browser storage.");
    }
    setKeys((items) => [saved.key, ...items.map((item) => saved.key.is_default ? { ...item, is_default: false } : item)]);
    setPassphrase("");
    setConfirm("");
    setKeyModal(null);
    addToast(storage === "browser" ? "Key generated and saved in this browser." : "Key generated and stored server-side with its passphrase protection.");
  }

  async function importKey() {
    const parsed = await privateKeyMetadata(importArmored.trim());
    const saved = await api.saveKey(csrf, {
      label: keyLabel || "Imported OpenPGP key",
      fingerprint: parsed.fingerprint,
      public_key_armored: parsed.publicKeyArmored,
      encrypted_private_key: storage === "server" ? parsed.privateKeyArmored : "",
      storage_mode: storage,
      is_default: keys.length === 0
    });
    if (storage === "browser") await saveBrowserPGPKey(saved.key.id, parsed.privateKeyArmored);
    setKeys((items) => [saved.key, ...items.map((item) => saved.key.is_default ? { ...item, is_default: false } : item)]);
    setImportArmored("");
    setKeyModal(null);
    addToast(storage === "browser" ? "Private key imported in this browser." : "Private key imported to server storage.");
  }

  async function importKeyFile(file: File) {
    setImportArmored(await file.text());
  }

  async function markDefault(key: EncryptionKey) {
    const data = await api.setDefaultKey(csrf, key.id);
    setKeys(data.keys || []);
    addToast("Default key updated.");
  }

  async function downloadPrivate(key: EncryptionKey) {
    let armored = key.encrypted_private_key || "";
    if (!armored && key.storage_mode === "browser") armored = await loadBrowserPGPKey(key.id);
    if (!armored) {
      addToast("No private key material is saved for this key. Import it again or generate a new key if you need export.", "error");
      return;
    }
    if (!window.confirm("Downloading a private key exposes the secret needed to decrypt your encrypted notes. Store it somewhere secure and do not share it. Continue?")) return;
    downloadKey(`cairnfield-${key.fingerprint || key.id}-private.asc`, armored);
  }

  async function startBackup() {
    setBackupBusy(true);
    try {
      const data = await api.startBackup(csrf);
      setBackups((items) => [data.backup, ...items.filter((item) => item.id !== data.backup.id)]);
      addToast("Backup started.");
    } finally {
      setBackupBusy(false);
    }
  }

  return (
    <>
    <div className="settings-grid">
      <section className="panel profile-settings-panel">
        <h1>Profile</h1>
        <p className="muted">Choose how older absolute dates are shown. Recent sidebar activity still uses minutes and hours ago.</p>
        <form className="profile-settings-form" onSubmit={(event) => { event.preventDefault(); void saveProfile().catch((err) => addToast(messageFromError(err), "error")); }}>
          <label>Date format
            <select value={dateFormat} onChange={(event) => setDateFormat(event.target.value)}>
              {dateFormatOptions.map((option) => <option value={option.value} key={option.value}>{option.label} · {option.sample}</option>)}
            </select>
          </label>
          <button type="submit" disabled={profileSaving || dateFormat === (user.date_format || "ymd_slash")}><FloppyDiskIcon />{profileSaving ? "Saving..." : "Save profile"}</button>
        </form>
      </section>
      <section className="panel template-editor-panel">
        <div className="template-head">
          <div><h1>Templates</h1><p className="muted">Shape the title, folder, and starting body used when you create notes.</p></div>
          <button type="button" className="secondary" onClick={resetTemplate}><FilePlusIcon />New template</button>
        </div>
        <div className="template-editor">
          <aside className="template-list" aria-label="Templates">
            {(templates || []).length === 0 ? <div className="empty-key-row"><FileTextIcon />No templates yet</div> : (templates || []).map((item) => (
              <button type="button" className={`template-list-row ${template.id === item.id ? "active" : ""}`} key={item.id} onClick={() => setTemplate(item)}>
                <span><strong>{item.name}</strong><small>{item.folder_template || "Selected folder"} · {item.title_template || "Untitled"}{item.create_once ? " · one per title" : ""}</small></span>
                {item.is_default ? <StarIcon weight="fill" /> : null}
              </button>
            ))}
          </aside>
          <form className="template-form" onSubmit={(event) => { event.preventDefault(); void saveTemplate().catch((err) => addToast(messageFromError(err), "error")); }}>
            <div className="template-fields">
              <label>Name<input value={template.name || ""} placeholder="Daily note" onChange={(event) => setTemplate({ ...template, name: event.target.value })} /></label>
              <label>Title<input value={template.title_template || ""} placeholder="Untitled" onChange={(event) => setTemplate({ ...template, title_template: event.target.value })} /></label>
              <label>Folder<input value={template.folder_template || ""} placeholder="Selected folder" onChange={(event) => setTemplate({ ...template, folder_template: event.target.value })} /></label>
              <label className="check template-default"><input type="checkbox" checked={Boolean(template.is_default)} onChange={(event) => setTemplate({ ...template, is_default: event.target.checked })} />Default template</label>
              <label className="check template-default"><input type="checkbox" checked={Boolean(template.create_once)} onChange={(event) => setTemplate({ ...template, create_once: event.target.checked })} />Open existing note with this title</label>
            </div>
            <label>Body<textarea className="template-body-input" rows={10} value={template.body_template || ""} placeholder="Start with markdown..." onChange={(event) => setTemplate({ ...template, body_template: event.target.value })} /></label>
            <div className="template-preview">
              <div className="template-preview-meta"><span>{template.folder_template ? renderTemplatePreview(template.folder_template) : "Selected folder"}</span><strong>{renderTemplatePreview(template.title_template || "Untitled")}</strong></div>
              <pre>{renderTemplatePreview(template.body_template || "") || "Empty body"}</pre>
            </div>
            <div className="template-actions">
              {template.id ? <button type="button" className="secondary" onClick={() => void deleteTemplate().catch((err) => addToast(messageFromError(err), "error"))}><TrashIcon />Delete</button> : null}
              <button type="submit" disabled={templateSaving}><FloppyDiskIcon />{templateSaving ? "Saving..." : "Save template"}</button>
            </div>
            <div className="template-token-bar"><code>{"{date}"}</code><code>{"{datetime}"}</code><code>{"{year}"}</code><code>{"{month}"}</code><code>{"{day}"}</code><code>{"{sequence}"}</code></div>
          </form>
        </div>
      </section>
      <section className="panel">
        <h1>PGP keys</h1>
        <p className="muted">Public keys are saved on the server. Private keys can stay in this browser only, or be stored as a passphrase-protected server copy for unlock and export from other browsers.</p>
        <div className="key-manager rolltop-key-manager">
          <div className="key-list">
            {(keys || []).length === 0 ? <div className="empty-key-row"><LockIcon />No PGP keys yet</div> : (keys || []).map((key) => (
              <div className={`key-row ${key.is_default ? "default" : ""}`} key={key.id}>
                <LockIcon className="key-row-icon" />
                <div className="key-row-head">
                  <div><strong>{key.label}</strong><small>{shortFingerprint(key.fingerprint)} · {key.storage_mode === "browser" ? "Private key in this browser" : "Private key server-stored"} · {key.is_default ? "Default key" : "Available key"}</small></div>
                  <div className="key-actions">
                    {!key.is_default ? <button type="button" className="icon-only secondary" title="Make default" aria-label="Make default" onClick={() => void markDefault(key).catch((err) => addToast(messageFromError(err), "error"))}><StarIcon /></button> : null}
                    <button type="button" className="icon-only secondary" title="Download public key" aria-label="Download public key" onClick={() => downloadKey(`cairnfield-${key.fingerprint || key.id}-public.asc`, key.public_key_armored)}><DownloadSimpleIcon /></button>
                    <button type="button" className="icon-only secondary" title="Download private key" aria-label="Download private key" onClick={() => void downloadPrivate(key).catch((err) => addToast(messageFromError(err), "error"))}><KeyIcon /></button>
                  </div>
                </div>
              </div>
            ))}
          </div>
          <section className="key-storage-panel">
            <h2>Private key storage for new keys</h2>
            <label className={`storage-choice ${storage === "browser" ? "active" : ""}`}>
              <input type="radio" checked={storage === "browser"} onChange={() => setStorage("browser")} />
              <span><strong>This browser only</strong><small>Best server compromise: Cairnfield saves the public key, while this browser keeps the private key. Other browsers must import the same private key before they can decrypt.</small></span>
            </label>
            <label className={`storage-choice ${storage === "server" ? "active" : ""}`}>
              <input type="radio" checked={storage === "server"} onChange={() => setStorage("server")} />
              <span><strong>Server-stored copy</strong><small>More convenient across browsers. The server stores armored private key material; your PGP passphrase is still required in the browser.</small></span>
            </label>
          </section>
          <div className="key-workflows">
            <section className="key-workflow-panel">
              <h2>Import private key</h2>
              <p>Bring in an existing ASCII-armored private key from a file or pasted text.</p>
              <button type="button" className="secondary full-width" onClick={() => setKeyModal("import")}><UploadSimpleIcon />Import key</button>
            </section>
            <section className="key-workflow-panel">
              <h2>Generate private key</h2>
              <p>Create a new passphrase-protected key in this browser using the storage choice above.</p>
              <button type="button" className="secondary full-width" onClick={() => setKeyModal("generate")}><KeyIcon />Generate key</button>
            </section>
          </div>
        </div>
      </section>
      <section className="panel">
        <h1>Backups</h1>
        <p className="muted">Create a zip containing your current note markdown files, attached assets, and a manifest. Completed backups are available for 7 days.</p>
        <div className="backup-actions">
          <button type="button" className="secondary" disabled={backupBusy || backups.some((backup) => backup.status === "running")} onClick={() => void startBackup().catch((err) => addToast(messageFromError(err), "error"))}>
            <DownloadSimpleIcon />{backups.some((backup) => backup.status === "running") ? "Backup running..." : "Create backup"}
          </button>
          <button type="button" className="ghost" onClick={() => void loadBackups().catch((err) => addToast(messageFromError(err), "error"))}>Refresh</button>
        </div>
        <div className="backup-list">
          {backups.length === 0 ? <div className="empty-key-row"><DownloadSimpleIcon />No backups yet</div> : backups.map((backup) => (
            <div className={`backup-row ${backup.status}`} key={backup.id}>
              <div>
                <strong>{backup.filename}</strong>
                <small>{backup.status === "ready" ? `${formatBytes(backup.size)} - expires ${new Date(backup.expires_at).toLocaleString()}` : backup.status === "failed" ? backup.error || "Backup failed" : `Started ${new Date(backup.created_at).toLocaleString()}`}</small>
              </div>
              {backup.status === "ready" && backup.download_url ? <a className="button secondary" href={backup.download_url}><DownloadSimpleIcon />Download</a> : null}
            </div>
          ))}
        </div>
      </section>
    </div>
    {keyModal === "import" ? (
      <div className="modal-backdrop" onClick={() => setKeyModal(null)}>
        <div className="security-dialog" role="dialog" aria-label="Import private key" onClick={(event) => event.stopPropagation()}>
          <div className="modal-head"><h2>Import private key</h2><button type="button" className="ghost" onClick={() => setKeyModal(null)}>Close</button></div>
          <p className="muted">Paste, drop, or choose an ASCII-armored PGP private key.</p>
          <label>Key label<input value={keyLabel} onChange={(event) => setKeyLabel(event.target.value)} /></label>
          <label>Private key<textarea rows={8} value={importArmored} placeholder="-----BEGIN PGP PRIVATE KEY BLOCK-----" onChange={(event) => setImportArmored(event.target.value)} /></label>
          <div className="modal-actions">
            <label className="toolbar-upload"><UploadSimpleIcon />Choose file<input type="file" accept=".asc,.pgp,.txt" onChange={(event) => { const file = event.target.files?.[0]; if (file) void importKeyFile(file); }} /></label>
            <button type="button" className="secondary" onClick={() => setKeyModal(null)}>Cancel</button>
            <button type="button" disabled={!importArmored.trim()} onClick={() => void importKey().catch((err) => addToast(messageFromError(err), "error"))}>Import key</button>
          </div>
        </div>
      </div>
    ) : null}
    {keyModal === "generate" ? (
      <div className="modal-backdrop" onClick={() => setKeyModal(null)}>
        <div className="security-dialog" role="dialog" aria-label="Generate private key" onClick={(event) => event.stopPropagation()}>
          <div className="modal-head"><h2>Generate private key</h2><button type="button" className="ghost" onClick={() => setKeyModal(null)}>Close</button></div>
          <p className="muted">Create a passphrase-protected key and save it using the storage choice from Settings. Download backups from the key row after it is saved.</p>
          <label>Key label<input value={keyLabel} onChange={(event) => setKeyLabel(event.target.value)} /></label>
          <label>Passphrase<input type="password" value={passphrase} autoComplete="new-password" onChange={(event) => setPassphrase(event.target.value)} /></label>
          <label>Confirm passphrase<input type="password" value={confirm} autoComplete="new-password" onChange={(event) => setConfirm(event.target.value)} /></label>
          {issues.length > 0 ? <div className="notice subtle">{issues[0]}</div> : null}
          <div className="modal-actions">
            <button type="button" className="secondary" onClick={() => setKeyModal(null)}>Cancel</button>
            <button type="button" onClick={() => void saveGeneratedKey().catch((err) => addToast(messageFromError(err), "error"))}><KeyIcon />Generate key</button>
          </div>
        </div>
      </div>
    ) : null}
    </>
  );
}

function AdminView({ csrf, addToast }: { csrf: string; addToast: (message: string, kind?: Toast["kind"]) => number }) {
  const [users, setUsers] = useState<User[]>([]);
  const [form, setForm] = useState({ email: "", name: "", password: "", is_admin: false });
  useEffect(() => { void api.adminUsers().then((data) => setUsers(data.users || [])); }, []);
  return <section className="panel"><h1>Users</h1><form className="grid" onSubmit={(event) => { event.preventDefault(); void api.createUser(csrf, form).then(() => api.adminUsers()).then((data) => { setUsers(data.users || []); setForm({ email: "", name: "", password: "", is_admin: false }); addToast("User created."); }).catch((err) => addToast(messageFromError(err), "error")); }}><label>Email<input value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} /></label><label>Name<input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label><label>Password<input type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} /></label><label className="check"><input type="checkbox" checked={form.is_admin} onChange={(event) => setForm({ ...form, is_admin: event.target.checked })} />Admin</label><button type="submit">Create user</button></form>{(users || []).map((user) => <div className="version-row" key={user.id}><span>{user.email}</span><strong>{user.is_admin ? "admin" : "user"}</strong></div>)}</section>;
}

function ToastStack({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  return <div className="toasts">{toasts.map((toast) => <button type="button" key={toast.id} className={`toast ${toast.kind}`} onClick={() => onDismiss(toast.id)}>{toast.message}</button>)}</div>;
}

function buildFolderTree(folders: FolderRecord[], notes: NoteSummary[]) {
  const root: FolderNode = { path: "/", name: "Root", children: [], notes: [], noteCount: 0 };
  const nodes = new Map<string, FolderNode>([["/", root]]);
  const ensure = (path: string) => {
    const normalized = normalizeFolderPath(path);
    if (nodes.has(normalized)) return nodes.get(normalized)!;
    const segments = normalized.split("/").filter(Boolean);
    let current = root;
    let currentPath = "";
    for (const segment of segments) {
      currentPath = `${currentPath}/${segment}`;
      let next = nodes.get(currentPath);
      if (!next) {
        next = { path: currentPath, name: segment, children: [], notes: [], noteCount: 0 };
        nodes.set(currentPath, next);
        current.children.push(next);
      }
      current = next;
    }
    return current;
  };
  for (const folder of folders || []) ensure(folder.path);
  for (const note of notes || []) ensure(note.folder_path || "/").notes.push(note);
  for (const node of nodes.values()) {
    node.children.sort((a, b) => a.name.localeCompare(b.name));
    node.notes.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }
  const countNotes = (node: FolderNode): number => {
    node.noteCount = node.notes.length + node.children.reduce((sum, child) => sum + countNotes(child), 0);
    return node.noteCount;
  };
  countNotes(root);
  return root;
}

function normalizeFolderPath(path: string) {
  const clean = (path || "/").trim().replace(/\\/g, "/").replace(/\/+/g, "/");
  const withSlash = clean.startsWith("/") ? clean : `/${clean}`;
  return withSlash.length > 1 ? withSlash.replace(/\/+$/, "") : "/";
}

function currentNoteTargetFolder(folder: string) {
  if (!folder || folder.startsWith("__")) return "/";
  return normalizeFolderPath(folder);
}

function joinFolderPath(parent: string, name: string) {
  const safeName = name.trim().replace(/\\/g, "/").replace(/^\/+|\/+$/g, "");
  return normalizeFolderPath(`${normalizeFolderPath(parent)}/${safeName}`);
}

function folderNameFromPath(path: string) {
  const normalized = normalizeFolderPath(path);
  return normalized.split("/").filter(Boolean).pop() || "";
}

function replaceFolderPath(path: string, source: string, target: string) {
  const normalized = normalizeFolderPath(path);
  const from = normalizeFolderPath(source);
  const to = normalizeFolderPath(target);
  if (normalized === from) return to;
  if (normalized.startsWith(`${from}/`)) return normalizeFolderPath(`${to}${normalized.slice(from.length)}`);
  return normalized;
}

function noteIDsFromDragEvent(event: React.DragEvent) {
  const noteIDs = (event.dataTransfer.getData("text/note-ids") || "")
    .split(",")
    .map((value) => Number(value))
    .filter(Boolean);
  const noteID = Number(event.dataTransfer.getData("text/note-id"));
  return noteIDs.length > 0 ? noteIDs : noteID ? [noteID] : [];
}

function expandAncestors(current: Set<string>, path: string) {
  const next = new Set(current);
  next.add("/");
  const segments = normalizeFolderPath(path).split("/").filter(Boolean);
  let currentPath = "";
  for (const segment of segments) {
    currentPath = `${currentPath}/${segment}`;
    next.add(currentPath);
  }
  return next;
}

function upsertFolder(items: FolderRecord[], folder: FolderRecord) {
  const path = normalizeFolderPath(folder.path);
  if ((items || []).some((item) => normalizeFolderPath(item.path) === path)) return items;
  return [...(items || []), { ...folder, path }].sort((a, b) => a.path.localeCompare(b.path));
}

function insertAtEnd(current: string, markdown: string) {
  return `${current.trimEnd()}\n\n${markdown}\n`;
}

function saveLabel(status: "idle" | "dirty" | "saving" | "saved" | "offline") {
  switch (status) {
    case "dirty": return "Unsaved";
    case "saving": return "Saving...";
    case "saved": return "Saved";
    case "offline": return "Offline queue";
    default: return "";
  }
}

function formatDate(value: string) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "";
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(time);
}

function formatSidebarDate(value: string, dateFormat: string) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "";
  const diff = Date.now() - time;
  if (diff >= 0 && diff < 60_000) return "now";
  if (diff >= 0 && diff < 60 * 60_000) return `${Math.max(1, Math.floor(diff / 60_000))}m ago`;
  if (diff >= 0 && diff < 24 * 60 * 60_000) return `${Math.max(1, Math.floor(diff / (60 * 60_000)))}h ago`;
  return formatAbsoluteDate(value, dateFormat);
}

function formatAbsoluteDate(value: string, dateFormat: string) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "";
  const date = new Date(time);
  const year = String(date.getFullYear());
  const month = pad2(date.getMonth() + 1);
  const day = pad2(date.getDate());
  switch (dateFormat) {
    case "mdy_slash":
      return `${month}/${day}/${year}`;
    case "dmy_slash":
      return `${day}/${month}/${year}`;
    case "iso":
      return `${year}-${month}-${day}`;
    case "long":
      return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" }).format(date);
    case "ymd_slash":
    default:
      return `${year}/${month}/${day}`;
  }
}

function pad2(value: number) {
  return String(value).padStart(2, "0");
}

function formatDateTime(value: string) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "";
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit" }).format(time);
}

function folderTitle(folder: string) {
  if (folder === "__trash") return "Trash";
  if (folder === "__starred") return "Starred";
  if (!folder) return "All Notes";
  return folder === "/" ? "All Notes" : folder;
}

function highlightPreview(preview: string, query: string) {
  const term = searchHighlightTerm(query);
  if (!term) return preview;
  const lower = preview.toLowerCase();
  const needle = term.toLowerCase();
  const index = lower.indexOf(needle);
  if (index < 0) return preview;
  const start = Math.max(0, index - 70);
  const end = Math.min(preview.length, index + term.length + 110);
  const snippet = `${start > 0 ? "... " : ""}${preview.slice(start, end)}${end < preview.length ? " ..." : ""}`;
  const snippetLower = snippet.toLowerCase();
  const snippetIndex = snippetLower.indexOf(needle);
  if (snippetIndex < 0) return snippet;
  return <>
    {snippet.slice(0, snippetIndex)}
    <mark>{snippet.slice(snippetIndex, snippetIndex + term.length)}</mark>
    {snippet.slice(snippetIndex + term.length)}
  </>;
}

function previewText(markdown: string) {
  return (markdown || "")
    .replace(/!\[([^\]]*)\]\([^)]+\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[#*_`>~-]+/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function isNoteTrashed(note: Note) {
  const value = note.trashed_at || "";
  if (!value || value.startsWith("0001-01-01")) return false;
  const time = Date.parse(value);
  return Number.isFinite(time) && time > 0;
}

function searchHighlightTerm(query: string) {
  const plain = query.split(/\s+/).find((part) => part && !part.includes(":"));
  return (plain || "").replace(/^"|"$/g, "");
}

function broadcastAppLock() {
  const message = { type: "CAIRNFIELD_LOCK_APP" };
  navigator.serviceWorker?.controller?.postMessage(message);
  if ("BroadcastChannel" in window) {
    const channel = new BroadcastChannel(appLockChannelName);
    channel.postMessage(message);
    channel.close();
  }
}

type DiffKind = "same" | "add" | "remove";
type DiffRow = { kind: DiffKind; text: string };

function diffLines(before: string, after: string): DiffRow[] {
  const left = before.split(/\r?\n/);
  const right = after.split(/\r?\n/);
  if (left.length * right.length > 250_000) {
    return [
      ...left.map((text) => ({ kind: "remove" as const, text })),
      ...right.map((text) => ({ kind: "add" as const, text }))
    ];
  }
  const table = Array.from({ length: left.length + 1 }, () => Array<number>(right.length + 1).fill(0));
  for (let i = left.length - 1; i >= 0; i -= 1) {
    for (let j = right.length - 1; j >= 0; j -= 1) {
      table[i][j] = left[i] === right[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
    }
  }
  const rows: DiffRow[] = [];
  let i = 0;
  let j = 0;
  while (i < left.length && j < right.length) {
    if (left[i] === right[j]) {
      rows.push({ kind: "same", text: left[i] });
      i += 1;
      j += 1;
    } else if (table[i + 1][j] >= table[i][j + 1]) {
      rows.push({ kind: "remove", text: left[i] });
      i += 1;
    } else {
      rows.push({ kind: "add", text: right[j] });
      j += 1;
    }
  }
  while (i < left.length) {
    rows.push({ kind: "remove", text: left[i] });
    i += 1;
  }
  while (j < right.length) {
    rows.push({ kind: "add", text: right[j] });
    j += 1;
  }
  return rows;
}

function diffPrefix(kind: DiffKind) {
  if (kind === "add") return "+";
  if (kind === "remove") return "-";
  return " ";
}

async function versionDiffContent(version: NoteVersion, encrypted: boolean, securityUnlock: SecurityUnlock | null) {
  const content = version.content || "";
  if (!encrypted || !looksEncrypted(content)) return content;
  if (!securityUnlock) throw new Error("Unlock the app before diffing encrypted versions.");
  return decryptText(content, securityUnlock.privateKeyArmored, securityUnlock.passphrase);
}

function versionAuthor(version: NoteVersion, currentUser: User) {
  if (version.user_name) return version.user_name;
  if (version.user_email) return version.user_email;
  if (version.user_id === currentUser.id) return currentUser.name || currentUser.email;
  return `User ${version.user_id}`;
}

function versionLabel(version: NoteVersion, currentUser: User) {
  const author = versionAuthor(version, currentUser);
  return `${new Date(version.created_at).toLocaleString()} · ${author}`;
}

function NoteTitle({ note, securityUnlocked }: { note: NoteSummary; securityUnlocked: boolean }) {
  if (!note.is_encrypted) return <>{note.title || "Untitled"}</>;
  if (securityUnlocked && !looksEncrypted(note.title)) return <>{note.title || "Untitled"}</>;
  return <EncryptedTitleOvals noteID={note.id} />;
}

function EncryptedTitleOvals({ noteID }: { noteID: number }) {
  const widths = encryptedTitleWidths(noteID);
  return (
    <span className="encrypted-title-ovals" aria-label="Encrypted title">
      {widths.map((width, index) => <span key={`${noteID}-${index}`} className="title-oval" style={{ width }} />)}
    </span>
  );
}

function encryptedTitleWidths(noteID: number) {
  let seed = (noteID || 1) * 2654435761;
  const count = 2 + (seed % 3);
  const widths: number[] = [];
  for (let index = 0; index < count; index += 1) {
    seed = (seed ^ (seed >>> 13)) * 1274126177;
    widths.push(18 + Math.abs(seed % 34));
  }
  return widths;
}

function preserveUnlockedTitles(items: NoteSummary[], titleCache: Map<number, string>) {
  if (titleCache.size === 0) return items;
  let changed = false;
  const next = (items || []).map((item) => {
    if (!item.is_encrypted || !looksEncrypted(item.title)) return item;
    const title = titleCache.get(item.id);
    if (!title) return item;
    changed = true;
    return { ...item, title };
  });
  return changed ? next : items;
}

async function decryptSummaryTitles(items: NoteSummary[], unlock: SecurityUnlock, titleCache: Map<number, string>) {
  const preserved = preserveUnlockedTitles(items, titleCache);
  const encrypted = (items || []).filter((item) => item.is_encrypted && looksEncrypted(item.title));
  if (encrypted.length === 0) return preserved;
  const titleByID = new Map<number, string>();
  await Promise.all(encrypted.map(async (item) => {
    try {
      const title = await decryptText(item.title, unlock.privateKeyArmored, unlock.passphrase);
      if (title) {
        titleByID.set(item.id, title);
        titleCache.set(item.id, title);
      }
    } catch {
      // Leave undecryptable rows blinded.
    }
  }));
  if (titleByID.size === 0) return preserved;
  return preserved.map((item) => {
    const title = titleByID.get(item.id);
    return title ? { ...item, title } : item;
  });
}

function shortFingerprint(value: string) {
  const clean = (value || "").replace(/\s+/g, "");
  if (clean.length <= 16) return clean || "No fingerprint";
  return `${clean.slice(0, 8)}...${clean.slice(-8)}`;
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`;
}

function renderTemplatePreview(value: string) {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const year = String(now.getFullYear());
  const month = pad(now.getMonth() + 1);
  const day = pad(now.getDate());
  return (value || "")
    .replaceAll("{date}", `${year}-${month}-${day}`)
    .replaceAll("{datetime}", `${year}-${month}-${day} ${pad(now.getHours())}:${pad(now.getMinutes())}`)
    .replaceAll("{year}", year)
    .replaceAll("{month}", month)
    .replaceAll("{day}", day)
    .replaceAll("{sequence}", "1")
    .replaceAll("\\n", "\n");
}

function encryptedAssetURLs(markdown: string) {
  const matches = new Set<string>();
  const pattern = /\/assets\/[a-z0-9]{8}\/[^)\s"']+/gi;
  for (const match of markdown.matchAll(pattern)) {
    matches.add(match[0]);
  }
  return Array.from(matches);
}

function revokeAssetURLMap(map: Map<string, string>) {
  map.forEach((url) => {
    if (url.startsWith("blob:")) URL.revokeObjectURL(url);
  });
}

function pruneAssetURLMap(map: Map<string, string>, keep: Set<string>) {
  const next = new Map<string, string>();
  map.forEach((objectURL, sourceURL) => {
    if (keep.has(sourceURL)) next.set(sourceURL, objectURL);
    else if (objectURL.startsWith("blob:")) URL.revokeObjectURL(objectURL);
  });
  return next;
}

function replaceAssetURLs(markdown: string, assetURLMap: Map<string, string>) {
  let out = markdown;
  assetURLMap.forEach((objectURL, sourceURL) => {
    out = out.split(sourceURL).join(objectURL);
  });
  return out;
}

function restoreAssetURLs(markdown: string, assetURLMap: Map<string, string>) {
  let out = markdown;
  assetURLMap.forEach((objectURL, sourceURL) => {
    out = out.split(objectURL).join(sourceURL);
  });
  return out;
}

function contentTypeFromAssetURL(url: string) {
  const clean = url.split("?")[0].toLowerCase();
  if (clean.endsWith(".png")) return "image/png";
  if (clean.endsWith(".jpg") || clean.endsWith(".jpeg")) return "image/jpeg";
  if (clean.endsWith(".gif")) return "image/gif";
  if (clean.endsWith(".webp")) return "image/webp";
  if (clean.endsWith(".svg")) return "image/svg+xml";
  return "application/octet-stream";
}

function bytesToArrayBuffer(bytes: Uint8Array) {
  const copy = new Uint8Array(bytes.byteLength);
  copy.set(bytes);
  return copy.buffer;
}

function fileDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === "string" ? resolve(reader.result) : reject(new Error("Could not read image preview"));
    reader.onerror = () => reject(reader.error || new Error("Could not read image preview"));
    reader.readAsDataURL(file);
  });
}

function looksEncrypted(value: string) {
  return /-----BEGIN PGP MESSAGE-----/.test(value || "");
}

function noteKeyFromLocation() {
  const parts = window.location.pathname.split("/").filter(Boolean);
  if (parts[0] !== "notes" || !parts[1]) return "";
  return parts[1];
}

function searchRouteFromLocation() {
  const parts = window.location.pathname.split("/").filter(Boolean);
  if (parts[0] !== "search") return { query: "", page: 1 };
  const params = new URLSearchParams(window.location.search);
  const exact = new URLSearchParams(window.location.search).get("q")?.trim();
  const query = exact || parts[1]?.replace(/-/g, " ").trim() || "";
  return { query, page: Math.max(1, Number(params.get("page")) || 1) };
}

function noteURL(note: Note) {
  const slug = note.slug || String(note.id);
  return `/notes/${slug}/${urlSegment(note.title)}`;
}

function searchURL(query: string, page = 1) {
  const trimmed = query.trim();
  const params = new URLSearchParams({ q: trimmed });
  if (page > 1) params.set("page", String(page));
  return `/search/${urlSegment(trimmed)}?${params}`;
}

function urlSegment(value: string) {
  const clean = value.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return clean || "untitled";
}
