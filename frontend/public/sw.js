const STATIC_CACHE = "cairnfield-static-v5";
const SCOPE_PATH = new URL(self.registration.scope).pathname.replace(/\/+$/, "");
const BASE_PATH = SCOPE_PATH === "/" ? "" : SCOPE_PATH;
const SHELL_URLS = [
  "/",
  "/manifest.webmanifest",
  "/icon.svg",
  "/favicon-32.png",
  "/apple-touch-icon.png",
  "/pwa-192.png",
  "/pwa-512.png"
].map((path) => withBase(path));

function withBase(path) {
  return `${BASE_PATH}${path}`;
}

function isAppAPI(pathname) {
  return pathname.startsWith(withBase("/api/")) || pathname.startsWith(withBase("/assets/"));
}

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
  if (isAppAPI(url.pathname)) return;

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request)
        .then((response) => {
          const copy = response.clone();
          caches.open(STATIC_CACHE).then((cache) => cache.put(withBase("/"), copy));
          return response;
        })
        .catch(() => caches.match(withBase("/")) || Response.error())
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
