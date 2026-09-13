# Post-merge follow-ups from the metaweb-surf-reads acceptance review

## Summary

IDBots accepted the fleet-scale MetaWeb Surf read path (branch `metaweb-surf-reads`, merged as `d7ca1e8`, deployed 2026-09-13) and listed post-merge follow-ups. None block the rollout; they are recorded here so they don't evaporate. Source: IDBots acceptance review, 2026-09-13.

## Items

1. **Revoked comments never retire in socialcontent** — the socialcontent read model processes confirmed create pins only; a revoked comment (modify/revoke pin targeting a paycomment) is not handled, so revoked comments stay in feeds and inbox rows.
2. **Revoked like pins unhandled in both models** — qa and socialcontent treat a like pin's last state as authoritative but a revoke of the like pin itself is not processed.
3. **Like rows excerpt wording** — inbox like rows return `excerpt: "like"` (and `"dislike"`), the spec originally said "empty for likes"; the spec was aligned to the implementation in the acceptance round, but decide whether the empty-string contract is preferable before more consumers ship.
4. **R7 hardening** — `SetTrustedProxies` (X-Forwarded-For spoofing currently bypasses per-IP buckets since ClientIP trusts the proxy chain), constant-time API-key token comparison, and bucket eviction for unbounded identity growth.
5. **Mempool-collapse test** — add an end-to-end test for the mempool → confirmed collapse on the fresh feed; also investigate the suspected pre-existing `isMempool` stickiness in `mergeConfirmedReplayWithPendingCurrent`.
6. **Test gaps** — duplicate-id collapse in `pins:batch`, mid-chain version resolution via the versions endpoint, re-like re-surfacing (covered indirectly; make it explicit), TTL expiry behavior of the version/fresh caches.

## Environment

- metaso-p2p: production `so.metaid.io` at `d7ca1e8`
- Date recorded: 2026-09-13
- Requested by: IDBots acceptance review
