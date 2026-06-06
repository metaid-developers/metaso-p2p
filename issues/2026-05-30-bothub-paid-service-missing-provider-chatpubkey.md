# Bothub paid service detail is missing provider chat public key

## Summary

Bothub buyer checkout needs the provider chat public key before it can send an encrypted order request through `/protocols/simplemsg`. Current local metaso-p2p paid service responses do not expose a provider chat key in either skill-service detail or the compatible user info endpoints, so Bothub cannot reach the Metalet payment prompt for paid orders.

This blocks the caller-side product flow:

1. User opens a paid service in Bothub.
2. User enters request text.
3. Bothub validates provider orderability before payment.
4. Validation fails with `Provider chat public key is not available`.
5. Metalet transfer is never opened, because paying before a sendable encrypted order would create a recoverability risk.

## Environment

- metaso-p2p: local service at `http://127.0.0.1:18091`
- Bothub: `main` after merge commit `a583c28`
- Date checked: 2026-05-30

## Evidence

Command used from Bothub/main:

```bash
node <<'NODE'
const base='http://127.0.0.1:18091';
const list=await (await fetch(`${base}/api/bot-hub/skill-service/list?size=100&chainName=mvc&sortBy=updated&order=desc&includeInactive=true`)).json();
const paid=(list.data?.list||[]).filter(s=>Number(s.price)>0 || String(s.price||'').trim() !== '' && String(s.price||'') !== '0');
for (const s of paid.slice(0, 10)) {
  const id=s.id||s.currentPinId||s.sourceServicePinId;
  const detail=await (await fetch(`${base}/api/bot-hub/skill-service/detail/${encodeURIComponent(id)}?chainName=mvc`)).json();
  console.log({
    name: s.name || s.displayName,
    id,
    price: s.price,
    currency: s.currency,
    provider: detail.data?.provider,
  });
}
NODE
```

Observed sample paid service:

```json
{
  "name": "Token消耗统计查询",
  "id": "940569ba432081bf3b7accfd5ef728daa58e1c78f792f2a8bd8d0779fa8c0464i0",
  "price": "0.0001",
  "currency": "SPACE",
  "provider": {
    "metaid": "1GkbHUvHhc9QtCwJCpavHAdeFM715xSkU6",
    "globalMetaId": "1GkbHUvHhc9QtCwJCpavHAdeFM715xSkU6",
    "address": "",
    "name": "BOT-007",
    "avatar": ""
  }
}
```

Fields checked recursively in `detail.data` for the first 10 paid services:

- `chatPubkey`
- `chatpubkey`
- `chatPublicKey`
- `chat_public_key`
- `chat_pubkey`
- `providerChatPubkey`
- `providerChatPublicKey`
- `pubkey`

Result: no matching field was present.

Profile fallback checks for sampled providers:

```text
GET /api/info/globalmetaid/1GkbHUvHhc9QtCwJCpavHAdeFM715xSkU6 -> code 40400, user not found
GET /api/info/address/1GkbHUvHhc9QtCwJCpavHAdeFM715xSkU6 -> code 40400, user not found
GET /api/info/metaid/1GkbHUvHhc9QtCwJCpavHAdeFM715xSkU6 -> code 1, returns basic profile, but no chat key
```

The same pattern was observed for other paid providers such as:

- `1EX5NN6npyCp3X6Sv4Yahv6DrBNKRtq4Gw`
- `17QEufa9n25HqWvwuqC7ucs7XTp4JnvMfo`

## Expected API contract

At least one reliable source should expose the provider chat public key needed to encrypt buyer orders:

Option A, preferred:

```ts
GET /api/bot-hub/skill-service/detail/:serviceId

data.provider = {
  metaid: string
  globalMetaId: string
  address: string
  name: string
  avatar: string
  chatPubkey: string
}
```

Option B:

```ts
GET /api/info/metaid/:metaid
GET /api/info/globalmetaid/:globalMetaId

data.chatPubkey = string
```

Either option lets Bothub validate that the provider can receive encrypted `/protocols/simplemsg` before starting paid transfer.

## Actual behavior

- Paid service detail returns provider identity and display fields but no chat public key.
- The returned `provider.globalMetaId` value appears to be accepted by `/api/info/metaid/:id`, not by `/api/info/globalmetaid/:id`.
- Compatible info endpoints do not expose chat key data for sampled providers.

## Product impact

- Free order preflight can reach Metalet `CreatePin`.
- Paid order cannot safely reach Metalet transfer because Bothub cannot encrypt the order request to the provider.
- Follow-up composer also remains disabled for sessions where the provider key cannot be recovered.

## Suggested fix

1. Add `chatPubkey` to `data.provider` in `/api/bot-hub/skill-service/detail/:serviceId`.
2. Consider also adding it to list items when cheap, so BotHub can show orderability before opening detail.
3. Normalize provider identity naming:
   - If the value is a legacy MetaID, expose it as `metaid`.
   - If `globalMetaId` is unavailable, leave it empty or omit it instead of copying the legacy MetaID into `globalMetaId`.
4. Expose the same chat key through `/api/info/metaid/:metaid` when known.

## Acceptance criteria

- At least one real paid service detail returns `data.provider.chatPubkey`.
- `GET /api/info/metaid/:providerMetaid` also returns `data.chatPubkey` when the provider has published one.
- Bothub paid request flow can pass provider-key validation and reach the Metalet transfer confirmation screen.
- Bothub follow-up composer can resolve the provider key for a paid order session after refresh.
