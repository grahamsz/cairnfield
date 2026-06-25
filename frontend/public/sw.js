const STATIC_CACHE = "cairnfield-static-v3";

self.addEventListener("install", (event) => {
  event.waitUntil(caches.delete(STATIC_CACHE));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(keys.map((key) => caches.delete(key)))));
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  return;
});

self.addEventListener("message", (event) => {
  if (!event.data || event.data.type !== "CAIRNFIELD_LOCK_APP") return;
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if (event.source && client.id === event.source.id) continue;
        client.postMessage({ type: "CAIRNFIELD_LOCK_APP" });
      }
    })
  );
});
