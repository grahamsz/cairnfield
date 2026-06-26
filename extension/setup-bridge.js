(function () {
  window.addEventListener("message", async (event) => {
    if (event.source !== window) return;
    const message = event.data || {};
    if (message.type !== "CAIRNFIELD_CLIPPER_SETUP") return;
    const baseUrl = String(message.baseUrl || "").trim().replace(/\/+$/, "");
    const token = String(message.token || "").trim();
    if (!baseUrl || !token.startsWith("cairnfield_")) {
      window.postMessage({ type: "CAIRNFIELD_CLIPPER_SETUP_RESULT", ok: false, error: "Invalid Cairnfield clipper setup payload." }, window.location.origin);
      return;
    }
    try {
      await chrome.storage.sync.set({ baseUrl });
      await chrome.storage.local.set({ token });
      window.postMessage({ type: "CAIRNFIELD_CLIPPER_SETUP_RESULT", ok: true }, window.location.origin);
    } catch (err) {
      window.postMessage({ type: "CAIRNFIELD_CLIPPER_SETUP_RESULT", ok: false, error: err.message || String(err) }, window.location.origin);
    }
  });
})();
