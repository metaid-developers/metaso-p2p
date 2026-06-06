# Bothub metaso-p2p Rename Requirements

## Purpose

`metaso-p2p` is the renamed successor of the backend previously referenced by
Bothub as `meta-socket`. Bothub is the only direct downstream frontend, so it
should update its naming, runtime configuration, smoke scripts, and user-facing
handoff text to the new product name.

This is a rename requirement only. API paths and response contracts are not
changed by the rename.

## Required Changes

1. Rename internal labels and docs from `meta-socket` to `metaso-p2p`.
2. Rename frontend runtime base-url env usage:

```bash
VITE_META_SOCKET_BASE_URL
```

to:

```bash
VITE_METASO_P2P_BASE_URL
```

3. Rename smoke-test env usage:

```bash
META_SOCKET_BASE_URL
```

to:

```bash
METASO_P2P_BASE_URL
```

4. Rename any smoke command or log label named `smoke:meta-socket` to
   `smoke:metaso-p2p`.
5. Once DNS is ready, point the public backend root to the new metaso-p2p host.
   The initial planned host is:

```text
https://so.metaid.io
```

6. Keep the API root as a service root. Do not mount metaso-p2p under
   `/chat-api`; Bothub should continue appending native API paths itself.

## API Compatibility

The backend rename does not change these Bothub-facing API surfaces:

- `GET /healthz`
- `GET /api/bot-hub/skill-service/list`
- `GET /api/bot-hub/skill-service/detail/:serviceId`
- `GET /api/private-chat/homes/:metaid`
- `GET /api/private-chat/messages`
- `GET /api/private-chat/messages/by-index`
- Socket.IO path: `/socket/socket.io`

## Acceptance Checks

Bothub should pass these checks after the rename:

```bash
METASO_P2P_BASE_URL=https://so.metaid.io pnpm smoke:metaso-p2p
```

For browser runtime:

```bash
VITE_METASO_P2P_BASE_URL=https://so.metaid.io
VITE_USE_AGGREGATOR_MOCK=false
VITE_USE_WS_MOCK=false
```

Expected result:

- health check returns HTTP 200;
- skill-service list returns real service rows;
- private-chat homes/messages routes still return the same contract shape;
- Socket.IO heartbeat/connectivity still works through `/socket/socket.io`;
- user-visible labels no longer say `meta-socket`.

## Notes

If final DNS differs from `https://so.metaid.io`, keep the variable names above
and replace only the value.
