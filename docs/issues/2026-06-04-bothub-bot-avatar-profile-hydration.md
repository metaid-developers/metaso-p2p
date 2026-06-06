# Bothub bot avatars need current profile hydration from Metaso P2P

Date: 2026-06-04
Consumer: Bothub homepage and online bot sidebar
Priority: P1
Status: open

## Summary

Bothub currently has to compensate for two Metaso P2P avatar/profile gaps:

1. `GET /api/bot-hub/skill-service/list` can return stale provider avatars.
2. `GET /socket/online/list` currently returns online app rows without any display profile or avatar fields.

Bothub can work around this by fetching file-indexer profile data per provider/bot, but Metaso P2P is the right aggregation boundary for BotHub-facing service and presence data. These endpoints should expose current, displayable avatar data directly.

## Current live evidence

### Stale provider avatar in skill-service aggregation

Request:

```bash
curl -sS 'https://socket.metaid.io/api/bot-hub/skill-service/list?size=300'
```

Observed Ellis Grant row:

```json
{
  "providerGlobalMetaId": "idq1wlsx9q3lf45uz3n654lnya8kplj6lt2vuwjgy5",
  "providerName": "Ellis Grant",
  "providerAvatar": "https://manapi.metaid.io/content/d20f8dc9512b55223e67ea6e6df7b664f24d788ef1f594cfc57cd43c557f5e8ci0"
}
```

The returned `providerAvatar` is stale and not currently displayable. The current file-indexer profile for the same globalMetaId resolves to:

```json
{
  "avatar": "/content/2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0",
  "avatarId": "2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0"
}
```

The displayable HTTP URL is:

```text
https://file.metaid.io/metafile-indexer/content/2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0
```

### Online list lacks profile/avatar data

Request:

```bash
curl -sS 'https://socket.metaid.io/socket/online/list?page=1&size=5'
```

Observed response shape:

```json
{
  "code": 0,
  "data": {
    "items": [
      {
        "metaid": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
        "type": "app",
        "connectedAt": 1780546569529
      },
      {
        "metaid": "12ghVWG1yAgNjzXj4mr3qK9DgyornMUikZ",
        "type": "app",
        "connectedAt": 1780546569821
      }
    ]
  }
}
```

There is no `userInfo`, `name`, `avatar`, `avatarId`, `avatarUrl`, `chatPublicKey`, or profile snapshot on these rows.

## Expected behavior

### Skill-service list/detail provider avatars

For every BotHub service provider, Metaso P2P should return a current display avatar derived from the latest usable profile source.

Recommended fields:

```json
{
  "providerAvatar": "https://file.metaid.io/metafile-indexer/content/<avatarPinId>",
  "providerAvatarId": "<avatarPinId>"
}
```

Requirements:

- Prefer current file-indexer profile avatar data over stale service-row avatar values.
- Treat `"/content/"`, empty strings, missing fields, and failed old `manapi` URLs as unusable placeholders.
- Return a browser-displayable HTTP URL, not just a raw pin, when avatar data exists.
- Keep existing field names backward compatible; adding `providerAvatarId` is fine.

### Online list app rows

For app presence rows, Metaso P2P should include a display profile snapshot or an explicitly documented equivalent.

Recommended shape:

```json
{
  "metaid": "idq...",
  "type": "app",
  "connectedAt": 1780546569529,
  "userInfo": {
    "globalMetaId": "idq...",
    "metaid": "<chain metaid hash if known>",
    "address": "<wallet address if known>",
    "name": "<display name>",
    "avatar": "/content/<avatarPinId>",
    "avatarId": "<avatarPinId>",
    "avatarUrl": "https://file.metaid.io/metafile-indexer/content/<avatarPinId>",
    "chatPublicKey": "<chat pubkey if known>",
    "bio": {}
  }
}
```

If Metaso P2P intentionally keeps `/socket/online/list` minimal, add a documented batch profile endpoint that Bothub can call with the returned identifiers instead of issuing one profile request per bot.

## Acceptance criteria

- Ellis Grant in `/api/bot-hub/skill-service/list?size=300` returns the current avatar pin `2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0` or its direct file-indexer content URL.
- `curl -L <providerAvatar>` returns image bytes when `providerAvatar` is present.
- `/socket/online/list` either includes profile/avatar data for app rows or the paired batch profile endpoint is documented and returns the same data.
- Bothub homepage should not need per-row file-indexer profile fallback just to show ordinary bot avatars.
