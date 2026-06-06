# Bothub paid service detail is missing payment metadata

## Summary

Bothub can load real local skill-service data from `http://127.0.0.1:18091`
and can complete the buyer-side free order broadcast path. The first real paid
native service tested cannot reach the safe Metalet transfer confirmation step
because the service detail has a non-zero `price` but empty payment metadata.

Bothub correctly stops before opening a wallet transfer because it has no
receiver address to pay.

## Environment

- Date checked: 2026-06-01 17:38 CST
- metaso-p2p base URL: `http://127.0.0.1:18091`
- Bothub dev URL: `http://127.0.0.1:5177/`
- Bothub env: `VITE_METASO_P2P_BASE_URL=/metaso-p2p`,
  `VITE_USE_AGGREGATOR_MOCK=false`, `VITE_USE_WS_MOCK=false`
- Wallet used for UI acceptance: `SunnyFung` /
  `idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0`

## Service Tested

- Display name: `紫微斗数算命 v2`
- Service name: `metabot-ziwei-fortune-v2`
- Service id:
  `09d5b9dc05b816d0d6f0641d03f8d42235cb162f9f76e3329805a0c4ca376669i0`
- Provider: `BOT-009`
- Provider globalMetaId: `125DQu9dBCXksYWg7HnmnmU3TpBNqnMsZF`

## API Evidence

```bash
curl 'http://127.0.0.1:18091/api/bot-hub/skill-service/detail/09d5b9dc05b816d0d6f0641d03f8d42235cb162f9f76e3329805a0c4ca376669i0?chainName=mvc'
```

Relevant payload:

```json
{
  "code": 0,
  "data": {
    "service": {
      "serviceName": "metabot-ziwei-fortune-v2",
      "displayName": "紫微斗数算命 v2",
      "price": "0.01",
      "currency": "SPACE",
      "settlementKind": "",
      "paymentChain": "",
      "paymentAddress": ""
    },
    "provider": {
      "globalMetaId": "125DQu9dBCXksYWg7HnmnmU3TpBNqnMsZF",
      "name": "BOT-009",
      "chatPubkey": "043b11b9dab85bbc2999274e9c2bf713bbf02dc86890e3d67e95fc15f05ea11e1d11a6c9ea4ff29b68b98595d1636be6a70ed1d5769cda5a4e08819e6dfdc8517e"
    },
    "schemaVersion": "botHubSkillServiceDetail.v1"
  }
}
```

The same service appears in the list endpoint with `price: "0.01"` and the
same empty `settlementKind`, `paymentChain`, and `paymentAddress` fields. Free
services in the same list do include native MVC payment metadata.

## Bothub UI Evidence

Chrome + Metalet acceptance steps:

1. Opened `http://127.0.0.1:5177/` with mocks disabled.
2. Connected the real Metalet wallet.
3. Selected `紫微斗数算命 v2`.
4. Entered a paid-flow safe-step QA prompt.
5. Review step showed provider `BOT-009` and price `0.01 SPACE`.
6. Clicking `Confirm & pay` stopped in Bothub with:
   `Service payment address is missing`.

No wallet transfer was opened and no payment was attempted.

## Expected Contract

The BotHub aggregation spec keeps payment metadata on `service`:

- `price`
- `currency`
- `paymentChain`
- `settlementKind`
- `paymentAddress`
- `mrc20Ticker`
- `mrc20Id`

The native examples in
`docs/specs/2026-05-28-bot-hub-skill-service-aggregation-api.md` include:

```json
{
  "price": "1",
  "currency": "SPACE",
  "settlementKind": "native",
  "paymentChain": "mvc",
  "paymentAddress": "18GED..."
}
```

## Impact

- Free order live broadcast can be attempted and the buyer workspace can reach
  a waiting order state.
- Paid native live acceptance remains blocked before the safe wallet transfer
  confirmation step.
- This is not a Bothub frontend defect. The frontend is refusing to pay an
  unknown receiver, which is the correct safety behavior.

## Suggested Fix

- Ensure active paid native services expose non-empty:
  - `settlementKind: "native"`
  - `paymentChain: "mvc"`
  - `paymentAddress: <provider or configured settlement receiver address>`
- Keep list and detail payment metadata consistent.
- If a paid service lacks valid settlement metadata, either omit it from the
  orderable list or expose a clear disabled/unorderable state so clients do not
  present it as payable.

## Acceptance Criteria

- The tested `metabot-ziwei-fortune-v2` detail returns non-empty
  `settlementKind`, `paymentChain`, and `paymentAddress`.
- Bothub paid request flow reaches the Metalet transfer confirmation showing
  `0.01 SPACE` to the expected receiver.
- Acceptance stops before clicking the final wallet payment confirmation.
