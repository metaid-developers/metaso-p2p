package ftmarket

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// SearchDocuments implements metaweb.DocumentSource, exposing FT token summaries
// ("ft-token") and active FT sell listings ("ft-listing") to GET /api/metaweb/search
// (Contract C2.2 / REQ-2A-02).
func (a *Aggregator) SearchDocuments() []metawebdoc.Document {
	if a == nil || a.store == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	summaries, _ := a.getOrBuildMarketSummaries(ctx)
	docs := make([]metawebdoc.Document, 0, len(summaries))

	for _, s := range summaries {
		if s == nil || s.Asset.AssetKey == "" {
			continue
		}
		title := firstNonEmpty(s.Asset.Symbol, s.Asset.TokenName, s.Asset.AssetKey)
		if s.Asset.Symbol != "" && s.Asset.TokenName != "" && !strings.EqualFold(s.Asset.Symbol, s.Asset.TokenName) {
			title = fmt.Sprintf("%s (%s)", s.Asset.Symbol, s.Asset.TokenName)
		}
		var tags []string
		if s.Asset.Symbol != "" {
			tags = append(tags, s.Asset.Symbol)
		}
		if s.Asset.Chain != "" {
			tags = append(tags, s.Asset.Chain)
		}
		if s.Asset.Standard != "" {
			tags = append(tags, s.Asset.Standard)
		}
		if s.Asset.CaVerified {
			tags = append(tags, "ca_verified")
		}
		var floorParts []string
		if s.Floors.BtcUnitSats != nil {
			floorParts = append(floorParts, fmt.Sprintf("btc floor %s sat", *s.Floors.BtcUnitSats))
		}
		if s.Floors.SpaceUnitSats != nil {
			floorParts = append(floorParts, fmt.Sprintf("space floor %s sat", *s.Floors.SpaceUnitSats))
		}
		if s.Floors.MeUnitMilli != nil {
			floorParts = append(floorParts, fmt.Sprintf("me floor %s mME", *s.Floors.MeUnitMilli))
		}
		floorParts = append(floorParts, fmt.Sprintf("listed %d, holders %d", s.ListedLotCount, s.HolderCount))
		summary := strings.Join(floorParts, " | ")

		docs = append(docs, metawebdoc.Document{
			ProtocolKey:    metawebdoc.KeyFtToken,
			SourcePinId:    s.Asset.AssetKey,
			CurrentPinId:   s.Asset.AssetKey,
			Fingerprint:    formatTokenVersionToken(s),
			ChainName:      s.Asset.Chain,
			Title:          title,
			Summary:        summary,
			Tags:           tags,
			ContentExcerpt: strings.TrimSpace(strings.Join([]string{s.Asset.Symbol, s.Asset.TokenName, s.Asset.AssetKey}, " ")),
			UpdatedAt:      a.nowUnix(),
			Extra: map[string]any{
				"asset":                    s.Asset,
				"holderCount":              s.HolderCount,
				"listedLotCount":           s.ListedLotCount,
				"listedTotalAmountDisplay": s.ListedTotalAmountDisplay,
				"floors":                   s.Floors,
				"recentGranularity":        s.RecentGranularity,
				"tradeStats24h":            s.TradeStats24h,
			},
		})
	}

	records, err := a.listAllEffectiveListings(a.zeroConfTrade)
	if err != nil {
		return docs
	}
	for _, rec := range records {
		if rec == nil || !rec.EffectiveActive || rec.Asset == nil || rec.Lot == nil {
			continue
		}
		dto := a.listingDTOFromRecord(rec)
		if dto == nil {
			continue
		}
		title := strings.TrimSpace(fmt.Sprintf("%s %s", dto.Lot.AmountDisplay, firstNonEmpty(dto.Asset.Symbol, dto.Asset.TokenName, dto.Asset.AssetKey)))
		var summaryParts []string
		if dto.UnitPrices != nil {
			if dto.UnitPrices.SpaceUnitSats != nil {
				summaryParts = append(summaryParts, *dto.UnitPrices.SpaceUnitSats+" sat/token (mvc-tokensell)")
			}
			if dto.UnitPrices.BtcUnitSats != nil {
				summaryParts = append(summaryParts, *dto.UnitPrices.BtcUnitSats+" sat/token (btc-psbt)")
			}
			if dto.UnitPrices.MeUnitMilli != nil {
				summaryParts = append(summaryParts, *dto.UnitPrices.MeUnitMilli+" mME/token (me-escrow)")
			}
		}
		if dto.Note != "" {
			summaryParts = append(summaryParts, dto.Note)
		}
		summary := strings.Join(summaryParts, " | ")

		var tags []string
		if dto.Asset.Symbol != "" {
			tags = append(tags, dto.Asset.Symbol)
		}
		if dto.Asset.Chain != "" {
			tags = append(tags, dto.Asset.Chain)
		}
		tags = append(tags, dto.AcceptedRails...)
		if dto.Asset.CaVerified {
			tags = append(tags, "ca_verified")
		}
		if dto.Seller.IsOnline {
			tags = append(tags, "online")
		}

		pinId := firstNonEmpty(dto.SellPinId, dto.ListingKey)
		docs = append(docs, metawebdoc.Document{
			ProtocolKey:           metawebdoc.KeyFtListing,
			SourcePinId:           pinId,
			CurrentPinId:          pinId,
			Fingerprint:           formatListingVersionToken(dto),
			ChainName:             firstNonEmpty(rec.ChainName, dto.Asset.Chain),
			Title:                 title,
			Summary:               summary,
			Tags:                  tags,
			ContentExcerpt:        firstNonEmpty(dto.Note, strings.TrimSpace(dto.Asset.TokenName+" "+dto.Asset.Symbol+" "+dto.Asset.AssetKey)),
			PublisherGlobalMetaId: dto.Seller.GlobalMetaId,
			PublisherMetaId:       rec.SellerMetaId,
			CreatedAt:             rec.CreatedAt,
			UpdatedAt:             rec.UpdatedAt,
			Extra: map[string]any{
				"listingKey":    dto.ListingKey,
				"asset":         dto.Asset,
				"lot":           dto.Lot,
				"rails":         dto.Rails,
				"acceptedRails": dto.AcceptedRails,
				"unitPrices":    dto.UnitPrices,
				"batchGroupId":  dto.BatchGroupId,
				"batchIndex":    dto.BatchIndex,
				"batchTotal":    dto.BatchTotal,
				"seller":        dto.Seller,
				"confirmed":     dto.Confirmed,
			},
		})
	}
	return docs
}

func ptrStrOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func formatTokenVersionToken(s *TokenMarketSummary) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%s#%d#%s#%s#%s#%s#%d#%t#%s",
		s.Asset.AssetKey,
		s.ListedLotCount,
		s.ListedTotalAmountDisplay,
		ptrStrOrEmpty(s.Floors.BtcUnitSats),
		ptrStrOrEmpty(s.Floors.SpaceUnitSats),
		ptrStrOrEmpty(s.Floors.MeUnitMilli),
		s.HolderCount,
		s.Asset.CaVerified,
		s.Asset.Symbol,
	)
}

func formatListingVersionToken(dto *ListingDTO) string {
	if dto == nil {
		return ""
	}
	var btc, space, me string
	if dto.UnitPrices != nil {
		btc = ptrStrOrEmpty(dto.UnitPrices.BtcUnitSats)
		space = ptrStrOrEmpty(dto.UnitPrices.SpaceUnitSats)
		me = ptrStrOrEmpty(dto.UnitPrices.MeUnitMilli)
	}
	return fmt.Sprintf("%s#%s#%t#%s#%s#%s#%t#%t#%t#%d#%d",
		dto.ListingKey,
		dto.SellPinId,
		dto.EffectiveActive,
		btc,
		space,
		me,
		dto.Asset.CaVerified,
		dto.Seller.IsOnline,
		dto.Confirmed,
		dto.ListedAt,
		dto.ExpiresAt,
	)
}
