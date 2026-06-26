import { bootstrap, getConfig } from "./shared.js";

const destination = document.querySelector("#destination");
const status = document.querySelector("#status");

function setStatus(message, kind = "") {
  status.textContent = message;
  status.className = kind;
}

function clipId() {
  return new URLSearchParams(location.search).get("clip") || "";
}

function activeDestination() {
  const option = destination.selectedOptions[0];
  return {
    folderPath: destination.value || "/",
    destinationKind: option?.dataset.kind || "folder"
  };
}

async function load() {
  try {
    const config = await getConfig();
    const data = await bootstrap();
    destination.innerHTML = "";
    for (const folder of data.folders || []) {
      const option = document.createElement("option");
      option.value = folder.path;
      option.dataset.kind = folder.display_mode === "moodboard" ? "board" : "folder";
      option.textContent = folder.display_mode === "moodboard" ? `${folder.path} (board)` : folder.path;
      destination.append(option);
    }
    if (config.lastDestination) destination.value = config.lastDestination;
    if (!destination.value && destination.options.length) destination.selectedIndex = 0;
  } catch (err) {
    setStatus(err.message || String(err), "error");
  }
}

async function send() {
  const id = clipId();
  if (!id) throw new Error("Missing image clip.");
  setStatus("Sending image...");
  const dest = activeDestination();
  const res = await chrome.runtime.sendMessage({
    type: "clip-pending-image",
    clipId: id,
    folderPath: dest.folderPath,
    destinationKind: dest.destinationKind
  });
  if (!res?.ok) throw new Error(res?.error || "Could not clip image.");
  setStatus("Image clipped.", "success");
  setTimeout(() => window.close(), 500);
}

document.querySelector("#send").addEventListener("click", () => void send().catch((err) => setStatus(err.message || String(err), "error")));
document.querySelector("#cancel").addEventListener("click", () => window.close());

void load();
