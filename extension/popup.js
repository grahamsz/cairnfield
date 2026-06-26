import { bootstrap, cairnfieldFetch, extensionNow, getConfig, saveDestination } from "./shared.js";

const destination = document.querySelector("#destination");
const status = document.querySelector("#status");

function setStatus(message, kind = "") {
  status.textContent = message;
  status.className = kind;
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

async function activeTab() {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tabs[0]?.id) throw new Error("No active tab.");
  return tabs[0];
}

async function capturePreview(tab, cropRect = null) {
  if (!tab.windowId) return null;
  try {
    const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: "png" });
    if (cropRect) return await cropDataUrl(dataUrl, cropRect);
    const res = await fetch(dataUrl);
    return { blob: await res.blob(), dataUrl };
  } catch {
    return null;
  }
}

async function cropDataUrl(dataUrl, rect) {
  const image = await imageFromDataURL(dataUrl);
  const scaleX = image.naturalWidth / Math.max(1, rect.viewportWidth || image.naturalWidth);
  const scaleY = image.naturalHeight / Math.max(1, rect.viewportHeight || image.naturalHeight);
  const padding = 8;
  const sx = Math.max(0, Math.floor((rect.left - padding) * scaleX));
  const sy = Math.max(0, Math.floor((rect.top - padding) * scaleY));
  const sw = Math.min(image.naturalWidth - sx, Math.ceil((rect.width + padding * 2) * scaleX));
  const sh = Math.min(image.naturalHeight - sy, Math.ceil((rect.height + padding * 2) * scaleY));
  if (sw <= 0 || sh <= 0) {
    const res = await fetch(dataUrl);
    return { blob: await res.blob(), dataUrl };
  }
  const canvas = document.createElement("canvas");
  canvas.width = sw;
  canvas.height = sh;
  const ctx = canvas.getContext("2d");
  ctx.drawImage(image, sx, sy, sw, sh, 0, 0, sw, sh);
  const croppedDataUrl = canvas.toDataURL("image/png");
  const res = await fetch(croppedDataUrl);
  return { blob: await res.blob(), dataUrl: croppedDataUrl };
}

function imageFromDataURL(dataUrl) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error("Could not load preview image."));
    image.src = dataUrl;
  });
}

function blobFromBase64(base64, contentType) {
  const binary = atob(base64);
  const chunkSize = 32768;
  const chunks = [];
  for (let offset = 0; offset < binary.length; offset += chunkSize) {
    const slice = binary.slice(offset, offset + chunkSize);
    const bytes = new Uint8Array(slice.length);
    for (let i = 0; i < slice.length; i += 1) bytes[i] = slice.charCodeAt(i);
    chunks.push(bytes);
  }
  return new Blob(chunks, { type: contentType });
}

async function capturePDF(tab) {
  const target = { tabId: tab.id };
  let attached = false;
  try {
    await chrome.debugger.attach(target, "1.3");
    attached = true;
    await chrome.debugger.sendCommand(target, "Page.enable");
    const result = await chrome.debugger.sendCommand(target, "Page.printToPDF", {
      printBackground: true,
      preferCSSPageSize: true,
      displayHeaderFooter: false,
      marginTop: 0.25,
      marginBottom: 0.25,
      marginLeft: 0.25,
      marginRight: 0.25
    });
    if (!result?.data) throw new Error("Chrome did not return PDF data.");
    return blobFromBase64(result.data, "application/pdf");
  } finally {
    if (attached) {
      try {
        await chrome.debugger.detach(target);
      } catch {
        // The tab may have closed or Chrome may already have detached.
      }
    }
  }
}

function visualArchiveHTML(metadata, screenshotDataUrl, reason = "") {
  const title = escapeHTML(metadata.title || "Clipped page");
  const source = escapeHTML(metadata.source_url || metadata.page_url || "");
  const captured = escapeHTML(metadata.captured_at || "");
  const selection = escapeHTML(metadata.selection_text || "");
  const searchText = escapeHTML(metadata.search_text || "");
  const reasonText = escapeHTML(reason || "Visual archive");
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">
  <title>${title}</title>
  ${source ? `<meta name="source-url" content="${source}">` : ""}
  ${captured ? `<meta name="captured-at" content="${captured}">` : ""}
  ${selection ? `<meta name="selection-text" content="${selection}">` : ""}
  ${searchText ? `<meta name="searchable-text" content="${searchText.slice(0, 4000)}">` : ""}
  ${reasonText ? `<meta name="archive-reason" content="${reasonText}">` : ""}
  <style>
    html, body { margin: 0; background: #fff; }
    main { min-height: 100vh; display: flex; align-items: flex-start; justify-content: center; background: #fff; }
    img { display: block; width: 100%; height: auto; max-width: none; background: #fff; }
    .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
  </style>
</head>
<body>
  <main>
    <img src="${screenshotDataUrl}" alt="${title}">
  </main>
  <section class="sr-only" aria-label="Clip metadata">
    <h1>${title}</h1>
    ${source ? `<p>Source: ${source}</p>` : ""}
    ${captured ? `<p>Captured: ${captured}</p>` : ""}
    ${selection ? `<blockquote>${selection}</blockquote>` : ""}
    ${searchText ? `<pre>${searchText}</pre>` : ""}
    ${reasonText ? `<p>${reasonText}</p>` : ""}
  </section>
</body>
</html>`;
}

function escapeHTML(value) {
  return String(value || "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[char]));
}

function shouldUseVisualArchive(tab, clip) {
  if (clip.captureWarning) return true;
  try {
    const host = new URL(tab.url || "").hostname.toLowerCase();
    if (/(^|\.)google\.[a-z.]+$/.test(host)) return true;
  } catch {
    // Keep the SingleFile archive when the URL is not parseable.
  }
  return false;
}

async function capture(mode) {
  setStatus("Injecting capture script...");
  const tab = await activeTab();
  await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    files: ["content-capture.js"]
  });
  setStatus(mode === "selection" ? "Capturing selection..." : "Capturing page...");
  const [{ result: clip }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: mode === "selection" ? () => window.cairnfieldCaptureSelection() : () => window.cairnfieldCapturePage()
  });
  if (!clip?.html) throw new Error("Nothing to clip.");
  setStatus("Capturing preview...");
  const preview = await capturePreview(tab, mode === "selection" ? clip.previewRect : null);
  if (mode === "selection" && clip.selectionRanges?.length) {
    await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: (ranges) => window.cairnfieldRestoreSelection?.(ranges),
      args: [clip.selectionRanges]
    });
  }
  setStatus("Uploading clip...");
  const dest = activeDestination();
  await saveDestination(dest.folderPath, dest.destinationKind);
  const metadata = {
    title: clip.title || tab.title || "Clipped page",
    source_url: tab.url || "",
    page_url: tab.url || "",
    selection_text: clip.selectionText || "",
    search_text: clip.searchText || clip.selectionText || "",
    folder_path: dest.folderPath,
    destination_kind: dest.destinationKind,
    captured_at: extensionNow()
  };
  let html = clip.html;
  const visualArchive = Boolean(preview?.dataUrl && shouldUseVisualArchive(tab, clip));
  if (visualArchive) {
    html = visualArchiveHTML(metadata, preview.dataUrl, clip.captureWarning || "Visual archive used because this page does not serialize reliably as static HTML.");
  }
  const form = new FormData();
  form.set("metadata", JSON.stringify(metadata));
  form.set("html", new Blob([html], { type: "text/html" }), "clip.html");
  if (preview?.blob) form.set("preview", preview.blob, "preview.png");
  await cairnfieldFetch("/api/clip/html", { method: "POST", body: form });
  setStatus(visualArchive ? "Clipped visual archive." : "Clipped.", "success");
}

async function capturePagePDF() {
  setStatus("Injecting capture script...");
  const tab = await activeTab();
  await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    files: ["content-capture.js"]
  });
  const [{ result: clip }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => ({
      title: document.title,
      selectionText: String(window.getSelection?.() || "").trim(),
      searchText: window.cairnfieldSearchablePageText?.() || document.body?.innerText || ""
    })
  });
  setStatus("Capturing PDF...");
  const pdf = await capturePDF(tab);
  setStatus("Capturing preview...");
  const preview = await capturePreview(tab);
  setStatus("Uploading PDF clip...");
  const dest = activeDestination();
  await saveDestination(dest.folderPath, dest.destinationKind);
  const metadata = {
    title: clip?.title || tab.title || "Clipped page",
    source_url: tab.url || "",
    page_url: tab.url || "",
    selection_text: clip?.selectionText || "",
    search_text: clip?.searchText || clip?.selectionText || "",
    folder_path: dest.folderPath,
    destination_kind: dest.destinationKind,
    captured_at: extensionNow()
  };
  const form = new FormData();
  form.set("metadata", JSON.stringify(metadata));
  form.set("pdf", pdf, "clip.pdf");
  form.set("content_type", "application/pdf");
  if (preview?.blob) form.set("preview", preview.blob, "preview.png");
  await cairnfieldFetch("/api/clip/pdf", { method: "POST", body: form });
  setStatus("Clipped PDF.", "success");
}

async function captureSelectionImage() {
  setStatus("Injecting capture script...");
  const tab = await activeTab();
  await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    files: ["content-capture.js"]
  });
  setStatus("Capturing selection...");
  const [{ result: clip }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => window.cairnfieldCaptureSelectionImage()
  });
  setStatus("Capturing selected area...");
  const preview = await capturePreview(tab, clip?.previewRect || null);
  if (clip?.selectionRanges?.length) {
    await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: (ranges) => window.cairnfieldRestoreSelection?.(ranges),
      args: [clip.selectionRanges]
    });
  }
  if (!preview?.blob) throw new Error("Could not capture the selected area.");
  setStatus("Uploading selection...");
  const dest = activeDestination();
  await saveDestination(dest.folderPath, dest.destinationKind);
  const metadata = {
    title: clip?.selectionText ? `${clip.selectionText.slice(0, 80)}${clip.selectionText.length > 80 ? "..." : ""}` : clip?.title || tab.title || "Clipped selection",
    source_url: tab.url || "",
    page_url: tab.url || "",
    selection_text: clip?.selectionText || "",
    search_text: clip?.searchText || clip?.selectionText || "",
    folder_path: dest.folderPath,
    destination_kind: dest.destinationKind,
    captured_at: extensionNow()
  };
  const form = new FormData();
  form.set("metadata", JSON.stringify(metadata));
  form.set("content_type", preview.blob.type || "image/png");
  form.set("image", preview.blob, "selection.png");
  await cairnfieldFetch("/api/clip/image", { method: "POST", body: form });
  setStatus("Clipped selection image.", "success");
}

document.querySelector("#clip-pdf").addEventListener("click", () => void capturePagePDF().catch((err) => setStatus(err.message || String(err), "error")));
document.querySelector("#clip-selection-image").addEventListener("click", () => void captureSelectionImage().catch((err) => setStatus(err.message || String(err), "error")));
document.querySelector("#options").addEventListener("click", () => chrome.runtime.openOptionsPage());

void load();
