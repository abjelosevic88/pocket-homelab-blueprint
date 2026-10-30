# Reverse proxy

Set `BASE_URL` to the public address and `SECURE_COOKIES=true` behind HTTPS. See [deploy/reverse-proxy/Caddyfile](../deploy/reverse-proxy/Caddyfile). `/api/*` is reachable for CI jobs, scripts and linked apps; they send their own bearer token.

Uploads can be large (up to `MAX_UPLOAD_MB`, 300 MB by default): raise the proxy's body limit if it has one (Caddy has none by default; nginx needs `client_max_body_size`).
