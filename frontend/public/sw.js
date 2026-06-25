const STATIC_CACHE = "cairnfield-static-v4";
const SHELL_URLS = [
  "/",
  "/manifest.webmanifest",
  "/icon.svg",
  "/favicon-32.png",
  "/apple-touch-icon.png",
  "/pwa-192.png",
  "/pwa-512.png"
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(STATIC_CACHE)
      .then((cache) => cache.addAll(SHELL_URLS))
      .finally(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== STATIC_CACHE).map((key) => caches.delete(key))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/assets/")) return;

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request)
        .then((response) => {
          const copy = response.clone();
          caches.open(STATIC_CACHE).then((cache) => cache.put("/", copy));
          return response;
        })
        .catch(() => caches.match("/") || Response.error())
    );
    return;
  }

  event.respondWith(
    caches.match(request).then((cached) => {
      const fetched = fetch(request).then((response) => {
        if (response.ok) {
          const copy = response.clone();
          caches.open(STATIC_CACHE).then((cache) => cache.put(request, copy));
        }
        return response;
      }).catch(() => cached || Response.error());
      return cached || fetched;
    })
  );
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
