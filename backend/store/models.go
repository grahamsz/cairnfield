package store

import "time"

var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"is_admin"`
	Theme        string    `json:"theme"`
	DateFormat   string    `json:"date_format"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Session struct {
	ID         int64
	UserID     int64
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type Folder struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Note struct {
	ID               int64     `json:"id"`
	OwnerUserID      int64     `json:"owner_user_id"`
	FolderPath       string    `json:"folder_path"`
	Title            string    `json:"title"`
	Slug             string    `json:"slug"`
	CurrentVersionID int64     `json:"current_version_id"`
	IsEncrypted      bool      `json:"is_encrypted"`
	IsShared         bool      `json:"is_shared"`
	IsStarred        bool      `json:"is_starred"`
	SharedPermission string    `json:"shared_permission,omitempty"`
	TrashedAt        time.Time `json:"trashed_at,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type NoteVersion struct {
	ID            int64     `json:"id"`
	NoteID        int64     `json:"note_id"`
	UserID        int64     `json:"user_id"`
	UserEmail     string    `json:"user_email,omitempty"`
	UserName      string    `json:"user_name,omitempty"`
	Content       string    `json:"content"`
	HeaderJSON    string    `json:"header_json"`
	BodySHA256    string    `json:"body_sha256"`
	BaseVersionID int64     `json:"base_version_id"`
	ClientID      string    `json:"client_id"`
	Conflicted    bool      `json:"conflicted"`
	CreatedAt     time.Time `json:"created_at"`
}

type NoteSummary struct {
	Note
	Preview string `json:"preview"`
}

type Template struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Name           string    `json:"name"`
	TitleTemplate  string    `json:"title_template"`
	FolderTemplate string    `json:"folder_template"`
	BodyTemplate   string    `json:"body_template"`
	IsDefault      bool      `json:"is_default"`
	CreateOnce     bool      `json:"create_once"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Share struct {
	NoteID       int64  `json:"note_id"`
	OwnerUserID  int64  `json:"owner_user_id"`
	SharedUserID int64  `json:"shared_user_id"`
	Permission   string `json:"permission"`
	Email        string `json:"email"`
	Name         string `json:"name"`
}

type Asset struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	UserID      int64     `json:"user_id"`
	NoteID      int64     `json:"note_id"`
	VersionID   int64     `json:"version_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	BlobPath    string    `json:"blob_path"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	Encrypted   bool      `json:"encrypted"`
	CreatedAt   time.Time `json:"created_at"`
}

type BackupExport struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Status      string    `json:"status"`
	FilePath    string    `json:"-"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type BackupNote struct {
	Note
	Version NoteVersion
}

type EncryptionKey struct {
	ID                  int64     `json:"id"`
	UserID              int64     `json:"user_id"`
	Label               string    `json:"label"`
	Fingerprint         string    `json:"fingerprint"`
	PublicKeyArmored    string    `json:"public_key_armored"`
	EncryptedPrivateKey string    `json:"encrypted_private_key,omitempty"`
	StorageMode         string    `json:"storage_mode"`
	IsDefault           bool      `json:"is_default"`
	CreatedAt           time.Time `json:"created_at"`
}

type SearchDocument struct {
	NoteID     int64
	UserID     int64
	Title      string
	FolderPath string
	Content    string
	HeaderJSON string
	UpdatedAt  time.Time
	Encrypted  bool
	Shared     bool
	HasImage   bool
}
