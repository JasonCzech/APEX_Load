self.addEventListener('install', event => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  if (url.pathname === '/sw-cached.json') {
    event.respondWith(new Response('{"cached_by":"service_worker","ok":true}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    }));
  }
});
