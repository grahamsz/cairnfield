export type User = {
  id: number;
  email: string;
  name: string;
  is_admin: boolean;
  theme: string;
  date_format: string;
};

export type Template = {
  id: number;
  user_id: number;
  name: string;
  title_template: string;
  folder_template: string;
  body_template: string;
  is_default: boolean;
  create_once: boolean;
};

export type FolderRecord = {
  id: number;
  user_id: number;
  path: string;
  created_at: string;
  updated_at: string;
};

export type Note = {
  id: number;
  owner_user_id: number;
  folder_path: string;
  title: string;
  slug: string;
  current_version_id: number;
  is_encrypted: boolean;
  is_shared: boolean;
  is_starred: boolean;
  shared_permission?: string;
  trashed_at?: string;
  updated_at: string;
  created_at: string;
};

export type NoteSummary = Note & {
  preview: string;
};

export type Asset = {
  id: number;
  slug: string;
  user_id: number;
  note_id: number;
  version_id: number;
  filename: string;
  content_type: string;
  blob_path: string;
  sha256: string;
  size: number;
  encrypted: boolean;
  created_at: string;
};

export type NoteVersion = {
  id: number;
  note_id: number;
  user_id: number;
  user_email?: string;
  user_name?: string;
  content: string;
  header_json: string;
  body_sha256: string;
  base_version_id: number;
  client_id: string;
  conflicted: boolean;
  created_at: string;
};

export type Share = {
  note_id: number;
  shared_user_id: number;
  permission: "read" | "write";
  email: string;
  name: string;
};

export type EncryptionKey = {
  id: number;
  label: string;
  fingerprint: string;
  public_key_armored: string;
  encrypted_private_key?: string;
  storage_mode: string;
  is_default: boolean;
  created_at: string;
};

export type Bootstrap = {
  users_exist: boolean;
  user: User | null;
  csrf: string;
  templates: Template[];
  auth_providers?: AuthProvider[];
};

export type AuthProvider = {
  id: string;
  name: string;
  login_url: string;
};

export type BackupExport = {
  id: number;
  status: "running" | "ready" | "failed";
  filename: string;
  size: number;
  error?: string;
  created_at: string;
  completed_at?: string;
  expires_at: string;
  download_url?: string;
};
