# Bothub Meta-Socket Endpoint Contract

Bothub must point at a metaso-p2p-owned root origin, not at
`https://api.idchat.io` and not at an idchat `/chat-api` prefix.

```dotenv
VITE_METASO_P2P_BASE_URL=https://<metaso-p2p-host>
VITE_USE_AGGREGATOR_MOCK=false
VITE_USE_WS_MOCK=false
```

`VITE_METASO_P2P_BASE_URL` is the root host. Bothub appends native paths such
as `/api/bot-hub/...` and `/api/private-chat/...`.

## Required Routes

- `GET /healthz`
- `GET /api/bot-hub/skill-service/list`
- `GET /api/bot-hub/skill-service/detail/:serviceId`
- `GET /api/private-chat/homes/:metaId`
- `GET /api/private-chat/messages`
- `GET /api/private-chat/messages/by-index`
- `GET /api/private-chat/paths`
- Socket.IO at `/socket/socket.io`

The canonical private-chat routes are aliases of the historical
`/api/group-chat/...` private-chat handlers and return identical envelopes.

## Deployment Shape

Use a reverse proxy or load balancer that forwards the whole root origin to
metaso-p2p. Do not mount metaso-p2p under `/chat-api`; that prefix is only an
idchat compatibility surface for old chat clients.

```nginx
location / {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
}

location /socket/socket.io/ {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
  proxy_set_header Host $host;
}
```

metaso-p2p answers CORS preflight globally, so browser clients can call it
directly from Bothub origins. Production operators may still restrict CORS at
the proxy layer if they own a fixed allow-list.

## Acceptance

From the Bothub repo:

```bash
METASO_P2P_BASE_URL=https://<metaso-p2p-host> pnpm smoke:metaso-p2p
```

Additional route checks:

```bash
curl 'https://<metaso-p2p-host>/healthz'
curl 'https://<metaso-p2p-host>/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc'
curl 'https://<metaso-p2p-host>/api/private-chat/homes/<buyer-metaId>'
curl 'https://<metaso-p2p-host>/api/private-chat/messages?metaId=<buyer-metaId>&otherMetaId=<provider-metaId>&cursor=&size=5'
curl 'https://<metaso-p2p-host>/api/private-chat/messages/by-index?metaId=<buyer-metaId>&otherMetaId=<provider-metaId>&startIndex=0&size=5'
```

The data directory must be the real indexed MVC Pebble store, not an empty
temporary data directory.
