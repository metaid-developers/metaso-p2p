# Bothub cannot hydrate AI_Sunny replies because provider chat identity differs from service identity

## Summary

Bothub can send a real free order to AI_Sunny's online service and IDChat shows
that AI_Sunny receives and replies to the order. However, Bothub Delivery cannot
hydrate the provider reply from the local meta-socket private-chat API because
the BotHub service detail exposes AI_Sunny as `1Grq...`, while the live IDChat
conversation uses `idq14hm...`.

This looks like a provider identity alias/canonical-chat-peer gap in the
meta-socket contract. Bothub is correctly using the provider identity returned
by `/api/bot-hub/skill-service/detail`, but that identity is not sufficient to
read the current provider conversation.

## Environment

- Date checked: 2026-06-01 19:32 CST
- meta-socket base URL: `http://127.0.0.1:18091`
- Bothub dev URL: `http://127.0.0.1:5177/`
- Bothub env: `VITE_META_SOCKET_BASE_URL=/meta-socket`,
  `VITE_USE_AGGREGATOR_MOCK=false`, `VITE_USE_WS_MOCK=false`
- Wallet used for UI acceptance: `SunnyFung` /
  `idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0`
- Public chat API used for comparison: `https://api.idchat.io/chat-api`

## Service Tested

- Display name: `MetaWeb/MetaID 百科全书`
- Service name: `metabot-metaid-wiki-service`
- Service id:
  `e9a7064693dfdcbea381c8355c3c91c0ba3947abee816287774729c432378e61i0`
- Provider: `AI_Sunny`
- Provider identity returned by local BotHub detail:
  `1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za`
- Live IDChat peer for the same provider:
  `idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz`

## Order Evidence

- Bothub order reference:
  `05b77d20aef740a7f97341d89577b14286fd2a0e960a0809f29c4e65b0865dca`
- Buyer simplemsg pin observed in public chat API:
  `5d429d59f5c984735d897be27f197abff44dc55fc757a8f2d22031241b6179c7i0`
- Provider status/reply pins observed in public chat API:
  - `320f76e4349f4481aa34400693e46caddf0f0aba06446bf86f82c260aef23ebfi0`
  - `1c1a3f663fc1262cc13564e952c964e39d7319780daebdc7950a32d80656ce95i0`
  - `e2973785248294b1b9457a5341444ceb13eee440a7d8e39f44203a277a569551i0`

IDChat displayed AI_Sunny's response:

```text
[ORDER_STATUS:5d429d59f5c984735d897be27f197abff44dc55fc757a8f2d22031241b6179c7]
确认收到你的BotHub订单，正在开始执行。MetaID是基于比特币网络的去中心化身份协议，用于管理和验证数字身份与数据。技能执行可能需要一些时间，请耐心等待最终结果。
```

## API Evidence

Local health is healthy:

```bash
curl http://127.0.0.1:18091/healthz
```

Result:

```json
{"code":0,"data":{"service":"meta-socket","status":"ok","version":"dev"},"message":"","processingTime":1780313535516}
```

Local service detail returns the provider/payment/chat identity as `1Grq...`:

```bash
curl 'http://127.0.0.1:18091/api/bot-hub/skill-service/detail/e9a7064693dfdcbea381c8355c3c91c0ba3947abee816287774729c432378e61i0?chainName=mvc'
```

Relevant payload:

```json
{
  "code": 0,
  "data": {
    "service": {
      "serviceName": "metabot-metaid-wiki-service",
      "displayName": "MetaWeb/MetaID 百科全书",
      "price": "0",
      "currency": "SPACE",
      "settlementKind": "native",
      "paymentChain": "mvc",
      "paymentAddress": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za"
    },
    "provider": {
      "metaid": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
      "globalMetaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
      "address": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
      "name": "AI_Sunny",
      "chatPubkey": "046a25523425b7b6c936c2279d95353605a38e53c7cfa46a801783c0e328cd3065f6ad60f1df06aefac234743958155b9f9f5673d2657087dcd2cf3830899b3b76"
    },
    "schemaVersion": "botHubSkillServiceDetail.v1"
  }
}
```

Local private-chat history using the service-detail provider id returns older
rows, but not the new order/status pins:

```bash
curl 'http://127.0.0.1:18091/api/group-chat/private-chat-list?metaId=idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0&otherMetaId=1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za&cursor=&size=5'
```

Result summary:

```json
{
  "code": 0,
  "data": {
    "total": 25,
    "list": [
      {
        "fromGlobalMetaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
        "toGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
        "timestamp": 1780312592,
        "blockHeight": 175637
      }
    ]
  }
}
```

Local private-chat history using the live IDChat peer id returns no rows:

```bash
curl 'http://127.0.0.1:18091/api/group-chat/private-chat-list?metaId=idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0&otherMetaId=idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz&cursor=&size=5'
```

Result:

```json
{"code":0,"data":{"total":0,"nextCursor":"","nextTimestamp":0,"list":null},"message":"","processingTime":1780313535544}
```

Public IDChat chat-api using the live peer does return the current order and
AI_Sunny replies:

```bash
curl 'https://api.idchat.io/chat-api/group-chat/private-chat-list?metaId=idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0&otherMetaId=idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz&cursor=&size=10'
```

Result summary:

```json
{
  "code": 0,
  "data": {
    "total": 10,
    "list": [
      {
        "fromGlobalMetaId": "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz",
        "toGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
        "pinId": "e2973785248294b1b9457a5341444ceb13eee440a7d8e39f44203a277a569551i0",
        "timestamp": 1780313024
      },
      {
        "fromGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
        "toGlobalMetaId": "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz",
        "pinId": "5d429d59f5c984735d897be27f197abff44dc55fc757a8f2d22031241b6179c7i0",
        "timestamp": 1780312999
      }
    ]
  }
}
```

## Bothub UI Evidence

Chrome at the local Bothub delivery URL shows the AI_Sunny order under:

```text
/delivery?order=idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0%3A1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za%3A05b77d20aef740a7f97341d89577b14286fd2a0e960a0809f29c4e65b0865dca
```

Current state:

- service: `metabot-metaid-wiki-service`
- provider: `AI_Sunny`
- status: `等待接单`
- timeline: `请求已发送`, `服务处理中`
- banner: `交付记录需要同步`
- saved request details are visible
- AI_Sunny's IDChat reply is not hydrated into Delivery

## Expected Contract

Meta-socket should provide one of the following:

1. `skill-service/detail` exposes a canonical private-chat peer id for the
   provider, for example `provider.chatGlobalMetaId` or
   `provider.privateChatGlobalMetaId`, and that id can be used with
   `/api/group-chat/private-chat-list`.
2. `provider.globalMetaId` is the canonical id that should be used for private
   chat hydration, and local private-chat history includes the current order and
   provider status rows under that id.
3. `/api/group-chat/private-chat-list` resolves aliases between the service
   provider id (`1Grq...`) and the current IDChat peer id (`idq14hm...`) so
   callers can use the identity returned by service detail.

## Impact

- Buyer-side sending works.
- AI_Sunny receives and responds to the order.
- Bothub Delivery cannot show the provider response from the local meta-socket
  runtime because it has no reliable provider chat peer id from the BotHub
  detail contract.

This is not a request for Bothub to point at `https://api.idchat.io/chat-api`
for BotHub marketplace data. Bothub should keep using the native meta-socket
`/api/bot-hub/*` and local/private-chat APIs; those APIs need a canonical
identity bridge for this provider.

## Acceptance Criteria

- The AI_Sunny service detail exposes the private-chat peer id that contains
  the current order/status rows, or the local private-chat endpoint resolves
  the returned provider id to the current chat rows.
- Using only local meta-socket APIs, Bothub can fetch the buyer order pin
  `5d429d59f5c984735d897be27f197abff44dc55fc757a8f2d22031241b6179c7i0`
  and at least one AI_Sunny status/reply pin for the same conversation.
- BotHub Delivery can move the AI_Sunny acceptance order beyond the saved
  request-only recovery view and display the provider response.
