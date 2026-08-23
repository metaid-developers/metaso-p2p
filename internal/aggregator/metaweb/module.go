// Package metaweb implements the read-only MetaWeb aggregation endpoints:
// the unified cross-protocol search (GET /api/metaweb/search) and the generic
// pin read (GET /api/metaweb/pin/:pinId). It indexes nothing itself and owns
// no Pebble data: search documents are injected from the publishedcontent and
// skillservice aggregators via setters (mirroring the botsearch composition
// pattern), pin resolution dispatches across those local namespaces and falls
// back to a MANAPI passthrough.
//
// See docs/specs/2026-08-23-metaweb-search-api.md and
// docs/specs/2026-08-23-metaweb-pin-read-api.md for the contract.
package metaweb

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Business error codes of the /api/metaweb/* contract. HTTP stays 200 for
// envelope compatibility with the other aggregation endpoints.
const (
	codeInvalidParam = 40000
	codeNotFound     = 40400
	codeUnavailable  = 50000
)

// DocumentSource supplies a warm search-document snapshot (one implementation
// each from publishedcontent and skillservice).
type DocumentSource interface {
	SearchDocuments() []metawebdoc.Document
}

// ProfileNamer resolves publisher name/avatar for the returned page only
// (userinfo aggregator; empty strings when the profile is unknown).
type ProfileNamer interface {
	ProfileNameAvatar(globalMetaId, metaId string) (name, avatar string)
}

// PinLookup resolves a publishedcontent record by any pin id in its version
// chain, across all published protocol paths and chains.
type PinLookup interface {
	LookupByAnyPinId(pinId string) (*publishedcontent.Record, error)
}

// ServicePinLookup resolves a skillservice record by any pin id in its
// version chain.
type ServicePinLookup interface {
	LookupServiceByAnyPinId(pinId string) (*skillservice.ServiceRecord, error)
}

// AssetURLResolver turns metafile:// attachment URIs into absolute fetchable
// URLs (skillservice.AssetResolver or equivalent).
type AssetURLResolver interface {
	Resolve(asset string) string
}

// RemotePinFetcher fetches one pin from MANAPI for the pin-read remote
// fallback. ErrRemotePinNotFound maps to 40400; any other error to 50000.
type RemotePinFetcher interface {
	FetchPin(pinId string) (*RemotePin, error)
}

// Aggregator is a read-only aggregator: HandleBlockPin/HandleMempoolPin are
// no-ops and it owns no Pebble data.
type Aggregator struct {
	notifyCh      chan *aggregator.NotifyEvent
	sources       []DocumentSource
	profileNamer  ProfileNamer
	pinLookup     PinLookup
	serviceLookup ServicePinLookup
	assetResolver AssetURLResolver
	remoteFetcher RemotePinFetcher
	now           func() int64 // unix milliseconds; test hook
}

func (a *Aggregator) Name() string { return "metaweb" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	if a.now == nil {
		a.now = func() int64 { return time.Now().UnixMilli() }
	}
	return nil
}

// HandleBlockPin is a no-op: metaweb aggregates nothing of its own.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

// HandleMempoolPin is a no-op: metaweb aggregates nothing of its own.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/metaweb/search", a.handleSearch)
	router.GET("/metaweb/pin/:pinId", a.handlePinRead)
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// SetDocumentSources injects the search-document snapshots (publishedcontent
// and skillservice).
func (a *Aggregator) SetDocumentSources(sources ...DocumentSource) {
	a.sources = sources
}

func (a *Aggregator) SetProfileNamer(namer ProfileNamer) {
	a.profileNamer = namer
}

// SetPinLookups injects the local pin-read namespaces: publishedcontent
// records and skillservice records, resolved in that order.
func (a *Aggregator) SetPinLookups(published PinLookup, service ServicePinLookup) {
	a.pinLookup = published
	a.serviceLookup = service
}

func (a *Aggregator) SetAssetResolver(resolver AssetURLResolver) {
	a.assetResolver = resolver
}

func (a *Aggregator) SetRemotePinFetcher(fetcher RemotePinFetcher) {
	a.remoteFetcher = fetcher
}
