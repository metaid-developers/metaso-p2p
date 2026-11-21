package mrc721

import (
	"fmt"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// SearchDocuments implements metaweb.DocumentSource, exposing MRC-721 collections
// ("mrc721-collection") and active listings ("mrc721-listing") to GET /api/metaweb/search
// (REQ-B-08).
func (a *Aggregator) SearchDocuments() []metawebdoc.Document {
	if a == nil || a.store == nil {
		return nil
	}

	summaries, _ := a.getOrBuildMarketSummaries()
	docs := make([]metawebdoc.Document, 0, len(summaries))

	a.mu.Lock()
	nftReader := a.nftReader
	zeroConf := a.zeroConfTrade
	a.mu.Unlock()

	colCreatorMetaId := make(map[string]string)
	if nftReader != nil {
		for _, col := range nftReader.ListAllCollections(zeroConf) {
			if col != nil && col.PinId != "" {
				colCreatorMetaId[strings.ToLower(col.PinId)] = col.CreateMetaId
			}
		}
	}

	for _, s := range summaries {
		if s == nil || s.CollectionPinId == "" {
			continue
		}
		title := firstNonEmpty(s.DisplayTitle, s.CollectionName, s.CollectionPinId)
		var tags []string
		if s.CaVerified {
			tags = append(tags, "ca_verified")
		}
		if s.CollectionName != "" {
			tags = append(tags, s.CollectionName)
		}
		summary := fmt.Sprintf("listed %d, holders %d", s.ListedCount, s.HolderCount)
		if s.FloorPriceMeMilli != nil {
			summary = fmt.Sprintf("floor %.3f ME, %s", float64(*s.FloorPriceMeMilli)/1000.0, summary)
		}
		if s.Desc != "" {
			summary = s.Desc + " | " + summary
		}
		docs = append(docs, metawebdoc.Document{
			ProtocolKey:           metawebdoc.KeyMrc721Collection,
			SourcePinId:           s.CollectionPinId,
			CurrentPinId:          s.CollectionPinId,
			Fingerprint:           formatMarketVersionToken(s),
			ChainName:             "mvc",
			Title:                 title,
			Summary:               summary,
			Tags:                  tags,
			ContentExcerpt:        s.Desc,
			PublisherGlobalMetaId: colCreatorMetaId[strings.ToLower(s.CollectionPinId)],
			CreatedAt:             s.CreateTime,
			UpdatedAt:             a.nowUnix(),
			Extra: map[string]any{
				"collectionPinId":     s.CollectionPinId,
				"collectionName":      s.CollectionName,
				"displayTitle":        s.DisplayTitle,
				"cover":               s.Cover,
				"caVerified":          s.CaVerified,
				"holderCount":         s.HolderCount,
				"burnedCount":         s.BurnedCount,
				"listedCount":         s.ListedCount,
				"floorPriceMeMilli":   s.FloorPriceMeMilli,
				"floorPriceCnyCents":  s.FloorPriceCnyCents,
				"medianPriceMeMilli":  s.MedianPriceMeMilli,
				"medianPriceCnyCents": s.MedianPriceCnyCents,
			},
		})
	}

	intents, err := a.listAllEffectiveSellIntents(true)
	if err != nil {
		return docs
	}
	for _, rec := range intents {
		if rec == nil || !rec.Active {
			continue
		}
		dto := a.listingDTOFromRecord(rec)
		title := firstNonEmpty(dto.ItemName, dto.CollectionName, dto.ItemPinId)
		var priceParts []string
		if dto.Prices != nil && dto.Prices.Me != nil && dto.Prices.Me.AmountMilli > 0 {
			priceParts = append(priceParts, fmt.Sprintf("%.3f ME", float64(dto.Prices.Me.AmountMilli)/1000.0))
		}
		if dto.Prices != nil && dto.Prices.Fiat != nil && dto.Prices.Fiat.AmountCents > 0 {
			priceParts = append(priceParts, fmt.Sprintf("%.2f %s", float64(dto.Prices.Fiat.AmountCents)/100.0, dto.Prices.Fiat.Currency))
		}
		if dto.Prices != nil {
			for _, tok := range dto.Prices.Tokens {
				label := firstNonEmpty(tok.AmountDisplay, tok.AmountBase)
				sym := firstNonEmpty(tok.Symbol, tok.AssetKey)
				if label != "" && sym != "" {
					priceParts = append(priceParts, label+" "+sym)
				}
			}
		}
		summary := strings.Join(priceParts, " / ")
		if dto.Note != "" {
			if summary != "" {
				summary += " - "
			}
			summary += dto.Note
		}
		var tags []string
		if dto.CollectionName != "" {
			tags = append(tags, dto.CollectionName)
		}
		if dto.CaVerified {
			tags = append(tags, "ca_verified")
		}
		if dto.Seller.IsOnline {
			tags = append(tags, "online")
		}
		if dto.Prices != nil {
			for _, tok := range dto.Prices.Tokens {
				if tok.Symbol != "" {
					tags = append(tags, tok.Symbol)
				}
				if tok.AssetKey != "" {
					tags = append(tags, tok.AssetKey)
				}
			}
		}
		docs = append(docs, metawebdoc.Document{
			ProtocolKey:           metawebdoc.KeyMrc721Listing,
			SourcePinId:           dto.IntentPinId,
			CurrentPinId:          dto.IntentPinId,
			Fingerprint:           formatListingVersionToken(dto),
			ChainName:             firstNonEmpty(rec.ChainName, "mvc"),
			Title:                 title,
			Summary:               summary,
			Tags:                  tags,
			ContentExcerpt:        firstNonEmpty(dto.Note, strings.TrimSpace(dto.ItemName+" "+dto.Desc), dto.Desc),
			PublisherGlobalMetaId: dto.Seller.GlobalMetaId,
			PublisherMetaId:       rec.SellerMetaId,
			CreatedAt:             rec.CreatedAt,
			UpdatedAt:             rec.UpdatedAt,
			Extra: map[string]any{
				"intentPinId":     dto.IntentPinId,
				"itemPinId":       dto.ItemPinId,
				"collectionPinId": dto.CollectionPinId,
				"collectionName":  dto.CollectionName,
				"caVerified":      dto.CaVerified,
				"prices":          dto.Prices,
				"seller":          dto.Seller,
				"cover":           dto.Cover,
			},
		})
	}
	return docs
}

func formatListingVersionToken(dto *ListingDTO) string {
	if dto == nil {
		return ""
	}
	var meMilli, cnyCents int64
	var fiatCur string
	var tokenFingerprints []string
	if dto.Prices != nil {
		if dto.Prices.Me != nil {
			meMilli = dto.Prices.Me.AmountMilli
		}
		if dto.Prices.Fiat != nil {
			fiatCur = dto.Prices.Fiat.Currency
			cnyCents = dto.Prices.Fiat.AmountCents
		}
		for _, tok := range dto.Prices.Tokens {
			tokenFingerprints = append(tokenFingerprints, tok.AssetKey+"="+tok.AmountBase)
		}
	}
	return fmt.Sprintf("%s#%s#%d#%s#%d#%s#%t#%t#%t#%t#%d#%d",
		dto.IntentPinId,
		dto.ItemPinId,
		meMilli,
		fiatCur,
		cnyCents,
		strings.Join(tokenFingerprints, ","),
		dto.AutoFulfillTokens,
		dto.CaVerified,
		dto.Seller.IsOnline,
		dto.Confirmed,
		dto.ListedAt,
		dto.ExpiresAt,
	)
}
