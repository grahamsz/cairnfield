import { appURL } from "./base";

export type PresenceParticipant = { user_id: number; name: string; email: string; same_user: boolean; editing: boolean; sessions: number };
export type PresenceMessage = { type: "presence"; note_id: number; participants: PresenceParticipant[] };
export type NoteSavedMessage = { type: "note_saved"; note_id: number; version_id: number; title: string; by_user_id: number; by_name: string; by_email: string; saved_at: number; content_sha256: string; editor_id: string };
export type PresenceServerMessage = PresenceMessage | NoteSavedMessage;

// Per-tab identifier sent with note saves; the server echoes it in note_saved
// broadcasts so this tab can skip its own save notifications.
export const editorID = crypto.randomUUID();

type PresenceHandler = (message: PresenceMessage) => void;
type NoteSavedHandler = (message: NoteSavedMessage) => void;
type WatchState = { noteID: number; editing: boolean };

const MIN_RECONNECT_DELAY = 1000;
const MAX_RECONNECT_DELAY = 30000;

class PresenceClient {
  private socket: WebSocket | null = null;
  private watchState: WatchState | null = null;
  private presenceHandlers = new Set<PresenceHandler>();
  private noteSavedHandlers = new Set<NoteSavedHandler>();
  private reconnectDelay = MIN_RECONNECT_DELAY;
  private reconnectTimer = 0;
  private closed = false;

  watch(noteID: number, editing: boolean) {
    const unchanged = this.watchState?.noteID === noteID && this.watchState.editing === editing;
    this.watchState = { noteID, editing };
    if (!unchanged) this.send({ type: "watch", note_id: noteID, editing });
  }

  unwatch(noteID: number) {
    if (this.watchState?.noteID === noteID) this.watchState = null;
    this.send({ type: "unwatch", note_id: noteID });
  }

  onPresence(cb: PresenceHandler) {
    this.presenceHandlers.add(cb);
    return () => this.presenceHandlers.delete(cb);
  }

  onNoteSaved(cb: NoteSavedHandler) {
    this.noteSavedHandlers.add(cb);
    return () => this.noteSavedHandlers.delete(cb);
  }

  close() {
    this.closed = true;
    this.watchState = null;
    window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = 0;
    const socket = this.socket;
    this.socket = null;
    socket?.close();
  }

  private connect() {
    if (this.closed || this.socket) return;
    const url = `${location.protocol === "https:" ? "wss://" : "ws://"}${location.host}${appURL("/ws")}`;
    const socket = new WebSocket(url);
    this.socket = socket;
    socket.onopen = () => {
      this.reconnectDelay = MIN_RECONNECT_DELAY;
      if (this.watchState) socket.send(JSON.stringify({ type: "watch", note_id: this.watchState.noteID, editing: this.watchState.editing }));
    };
    socket.onmessage = (event) => this.dispatch(event.data);
    socket.onclose = () => {
      if (this.socket === socket) this.socket = null;
      if (this.closed || !this.watchState) return;
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = window.setTimeout(() => {
        this.reconnectTimer = 0;
        if (this.watchState) this.connect();
      }, this.reconnectDelay);
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, MAX_RECONNECT_DELAY);
    };
    socket.onerror = () => socket.close();
  }

  private send(message: { type: "watch"; note_id: number; editing: boolean } | { type: "unwatch"; note_id: number }) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      // Lazy-connect on first watch; the open handler resends the current watch state.
      if (this.watchState) this.connect();
      return;
    }
    this.socket.send(JSON.stringify(message));
  }

  private dispatch(raw: unknown) {
    if (typeof raw !== "string") return;
    let message: PresenceServerMessage;
    try {
      message = JSON.parse(raw) as PresenceServerMessage;
    } catch {
      return;
    }
    if (message.type === "presence") this.presenceHandlers.forEach((cb) => cb(message));
    else if (message.type === "note_saved") this.noteSavedHandlers.forEach((cb) => cb(message));
  }
}

export const presenceClient = new PresenceClient();
