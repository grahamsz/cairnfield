import { bootstrap, getConfig, originPattern, saveConfig } from "./shared.js";

const form = document.querySelector("#options-form");
const baseUrl = document.querySelector("#base-url");
const token = document.querySelector("#token");
const status = document.querySelector("#status");

function setStatus(message, kind = "") {
  status.textContent = message;
  status.className = kind;
}

async function requestOriginPermission(value) {
  const pattern = originPattern(value);
  return chrome.permissions.request({ origins: [pattern] });
}

async function load() {
  const config = await getConfig();
  baseUrl.value = config.baseUrl;
  token.value = config.token;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("Saving...");
  try {
    await requestOriginPermission(baseUrl.value);
    await saveConfig({ baseUrl: baseUrl.value, token: token.value });
    setStatus("Saved.", "success");
  } catch (err) {
    setStatus(err.message || String(err), "error");
  }
});

document.querySelector("#test").addEventListener("click", async () => {
  setStatus("Testing...");
  try {
    await saveConfig({ baseUrl: baseUrl.value, token: token.value });
    await requestOriginPermission(baseUrl.value);
    const data = await bootstrap();
    setStatus(`Connected as ${data.user?.email || data.user?.name || "Cairnfield user"}.`, "success");
  } catch (err) {
    setStatus(err.message || String(err), "error");
  }
});

void load();
