import { cairnfieldFetch, extensionNow, getConfig, saveDestination } from "./shared.js";

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: "cairnfield-image",
    title: "Send image to Cairnfield",
    contexts: ["image"]
  });
});

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId !== "cairnfield-image") return;
  void prepareImageClip(info, tab).catch((err) => {
    chrome.notifications.create({
      type: "basic",
      iconUrl: "icons/icon128.png",
      title: "Cairnfield clip failed",
      message: err.message || String(err)
    });
  });
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type !== "clip-pending-image") return false;
  void clipPendingImage(message.clipId, message.folderPath, message.destinationKind)
    .then((result) => sendResponse({ ok: true, ...result }))
    .catch((err) => sendResponse({ ok: false, error: err.message || String(err) }));
  return true;
});

async function prepareImageClip(info, tab) {
  const config = await getConfig();
  if (!config.baseUrl || !config.token) {
    await chrome.runtime.openOptionsPage();
    return;
  }
  const imageUrl = info.srcUrl;
  if (!imageUrl) throw new Error("Image URL is missing.");
  const pageText = await pageSearchText(tab);
  const clipId = crypto.randomUUID();
  await chrome.storage.local.set({
    [`pendingImageClip:${clipId}`]: {
      imageUrl,
      pageUrl: info.pageUrl || tab?.url || "",
      title: imageTitle(imageUrl),
      searchText: pageText,
      createdAt: Date.now()
    }
  });
  await chrome.windows.create({
    url: chrome.runtime.getURL(`image-picker.html?clip=${encodeURIComponent(clipId)}`),
    type: "popup",
    width: 380,
    height: 290,
    focused: true
  });
}

async function clipPendingImage(clipId, folderPath, destinationKind) {
  const key = `pendingImageClip:${clipId}`;
  const item = (await chrome.storage.local.get(key))[key];
  if (!item?.imageUrl) throw new Error("Pending image clip expired.");
  const response = await fetch(item.imageUrl);
  if (!response.ok) throw new Error(`Could not read image: ${response.statusText}`);
  const blob = await response.blob();
  const targetFolder = folderPath || "/";
  const targetKind = destinationKind === "board" ? "board" : "folder";
  const metadata = {
    title: item.title || imageTitle(item.imageUrl),
    source_url: item.imageUrl,
    page_url: item.pageUrl || "",
    selection_text: "",
    search_text: item.searchText || "",
    folder_path: targetFolder,
    destination_kind: targetKind,
    captured_at: extensionNow()
  };
  const form = new FormData();
  form.set("metadata", JSON.stringify(metadata));
  form.set("content_type", blob.type || "image/png");
  form.set("image", blob, item.title || imageTitle(item.imageUrl));
  await cairnfieldFetch("/api/clip/image", { method: "POST", body: form });
  await saveDestination(targetFolder, targetKind);
  await chrome.storage.local.remove(key);
  chrome.notifications.create({
    type: "basic",
    iconUrl: "icons/icon128.png",
    title: "Image clipped",
    message: `Saved to ${targetFolder}.`
  });
  return { folderPath: targetFolder };
}

async function pageSearchText(tab) {
  if (!tab?.id) return "";
  try {
    await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      files: ["content-capture.js"]
    });
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: () => window.cairnfieldSearchablePageText?.() || document.body?.innerText || ""
    });
    return String(result || "").slice(0, 250000);
  } catch {
    return "";
  }
}

function imageTitle(value) {
  try {
    const url = new URL(value);
    const last = url.pathname.split("/").filter(Boolean).pop();
    return decodeURIComponent(last || "image");
  } catch {
    return "image";
  }
}
