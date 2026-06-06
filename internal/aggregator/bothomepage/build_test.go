package bothomepage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
)

type fakeProfileLookup struct {
	profile *ProfileSnapshot
	err     error
	seen    string
}

func (f *fakeProfileLookup) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	f.seen = globalMetaId
	return f.profile, f.err
}

type fakeServiceLister struct {
	result *skillservice.ListResult
	err    error
	params skillservice.ListParams
}

func (f *fakeServiceLister) List(params skillservice.ListParams) (*skillservice.ListResult, error) {
	f.params = params
	return f.result, f.err
}

type recordingServiceLister struct {
	gotParams skillservice.ListParams
	result    *skillservice.ListResult
	err       error
}

func (r *recordingServiceLister) List(params skillservice.ListParams) (*skillservice.ListResult, error) {
	r.gotParams = params
	return r.result, r.err
}

func TestBuildUserInfoLookupUnavailable(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(NewUserInfoLookupAdapter(nil))

	_, err := agg.Build("idqBot", DefaultOptions())
	if !errors.Is(err, ErrAggregationUnavailable) {
		t.Fatalf("Build error = %v, want ErrAggregationUnavailable", err)
	}
}

func TestBuildHomepageProfileDefaultModeAndPartialProofs(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.now = func() int64 { return 1780760000000 }
	agg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")

	lookup := &fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:    "idqCanonicalBotLongValue",
		MetaId:          "metaBot",
		Address:         "1BotAddress",
		Name:            "Fortune Bot",
		NameId:          "name-pin:i0",
		Avatar:          "/content/avatar-pin",
		AvatarId:        "avatar-pin",
		Background:      "/content/background-pin",
		BackgroundId:    "background-pin:i0",
		Bio:             "Reads the chain and answers directly.",
		BioId:           "bio-pin:i0",
		ChatPublicKey:   "02chatpubkey",
		ChatPublicKeyId: "chat-pin:i0",
		NftAvatar:       "nft-avatar-pin",
		ChainName:       "mvc",
	}}
	agg.SetProfileLookup(lookup)

	got, err := agg.Build("idqBot", DefaultOptions())
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if lookup.seen != "idqBot" {
		t.Fatalf("lookup globalMetaId = %q, want idqBot", lookup.seen)
	}
	if got.SchemaVersion != "botHomepage.v1" {
		t.Fatalf("SchemaVersion = %q, want botHomepage.v1", got.SchemaVersion)
	}
	if got.ResolvedAt != 1780760000000 {
		t.Fatalf("ResolvedAt = %d, want fixed clock", got.ResolvedAt)
	}
	if got.GlobalMetaId != "idqBot" {
		t.Fatalf("GlobalMetaId = %q, want request value", got.GlobalMetaId)
	}
	if got.Canonical.GlobalMetaId != "idqCanonicalBotLongValue" {
		t.Fatalf("Canonical.GlobalMetaId = %q", got.Canonical.GlobalMetaId)
	}
	if got.Canonical.MetaId != "metaBot" {
		t.Fatalf("Canonical.MetaId = %q", got.Canonical.MetaId)
	}
	if got.Canonical.Address != "1BotAddress" {
		t.Fatalf("Canonical.Address = %q", got.Canonical.Address)
	}
	if got.Canonical.ChainName != "mvc" {
		t.Fatalf("Canonical.ChainName = %q", got.Canonical.ChainName)
	}

	if got.Profile.Name != "Fortune Bot" {
		t.Fatalf("Profile.Name = %q", got.Profile.Name)
	}
	if got.Profile.Avatar != "https://file.metaid.io/metafile-indexer/content/avatar-pin" {
		t.Fatalf("Profile.Avatar = %q", got.Profile.Avatar)
	}
	if got.Profile.AvatarPinId != "avatar-pin" {
		t.Fatalf("Profile.AvatarPinId = %q", got.Profile.AvatarPinId)
	}
	if got.Profile.Background != "https://file.metaid.io/metafile-indexer/content/background-pin" {
		t.Fatalf("Profile.Background = %q", got.Profile.Background)
	}
	if got.Profile.BackgroundPinId != "background-pin:i0" {
		t.Fatalf("Profile.BackgroundPinId = %q", got.Profile.BackgroundPinId)
	}
	if got.Profile.ChatPubkey != "02chatpubkey" {
		t.Fatalf("Profile.ChatPubkey = %q", got.Profile.ChatPubkey)
	}
	if got.Profile.DisplayGlobalId == "" || got.Profile.DisplayGlobalId == got.Canonical.GlobalMetaId {
		t.Fatalf("Profile.DisplayGlobalId = %q, want abbreviated canonical id", got.Profile.DisplayGlobalId)
	}

	if got.Homepage.Mode != "default" {
		t.Fatalf("Homepage.Mode = %q", got.Homepage.Mode)
	}
	if got.Homepage.Title != "Fortune Bot" {
		t.Fatalf("Homepage.Title = %q", got.Homepage.Title)
	}
	if got.Homepage.Summary != "Reads the chain and answers directly." {
		t.Fatalf("Homepage.Summary = %q", got.Homepage.Summary)
	}
	if got.Homepage.Custom != nil {
		t.Fatalf("Homepage.Custom = %+v, want nil", got.Homepage.Custom)
	}

	if got.Presence.State != "unknown" {
		t.Fatalf("Presence.State = %q, want unknown", got.Presence.State)
	}
	if got.Presence.UpdatedAt != nil {
		t.Fatalf("Presence.UpdatedAt = %v, want nil", *got.Presence.UpdatedAt)
	}
	if got.Presence.Source != "" {
		t.Fatalf("Presence.Source = %q, want empty", got.Presence.Source)
	}
	if len(got.Services) != 0 {
		t.Fatalf("Services length = %d, want 0", len(got.Services))
	}

	if len(got.Actions) != 3 {
		t.Fatalf("Actions length = %d, want 3", len(got.Actions))
	}
	if got.Actions[0].Id != "message" || got.Actions[0].Kind != "private-chat" || !got.Actions[0].Enabled || !got.Actions[0].RequiresUsingIdentity {
		t.Fatalf("message action = %+v, want enabled private-chat requiring identity", got.Actions[0])
	}
	if got.Actions[1].Id != "services" || got.Actions[1].Kind != "service-list" || got.Actions[1].Enabled || !got.Actions[1].RequiresUsingIdentity {
		t.Fatalf("services action = %+v, want disabled service-list requiring identity", got.Actions[1])
	}
	if got.Actions[2].Id != "copy-uri" || got.Actions[2].Kind != "copy" || !got.Actions[2].Enabled || got.Actions[2].RequiresUsingIdentity || got.Actions[2].URI != "metaid://idqCanonicalBotLongValue" {
		t.Fatalf("copy-uri action = %+v", got.Actions[2])
	}

	if got.Proofs.VerificationState != "partial" {
		t.Fatalf("Proofs.VerificationState = %q, want partial", got.Proofs.VerificationState)
	}
	if got.Proofs.Identity != nil {
		t.Fatalf("Proofs.Identity = %+v, want nil", got.Proofs.Identity)
	}
	if got.Proofs.Homepage != nil {
		t.Fatalf("Proofs.Homepage = %+v, want nil", got.Proofs.Homepage)
	}
	if len(got.Proofs.Profile) == 0 {
		t.Fatal("expected non-empty profile proofs")
	}
	assertProfileProof(t, got.Proofs.Profile, "name", "/info/name", "name-pin:i0", "idqCanonicalBotLongValue")
	assertProfileProof(t, got.Proofs.Profile, "avatar", "/info/avatar", "avatar-pin", "idqCanonicalBotLongValue")
	assertProfileProof(t, got.Proofs.Profile, "background", "/info/background", "background-pin:i0", "idqCanonicalBotLongValue")
	assertProfileProof(t, got.Proofs.Profile, "bio", "/info/bio", "bio-pin:i0", "idqCanonicalBotLongValue")
	assertProfileProof(t, got.Proofs.Profile, "chatPubkey", "/info/chatpubkey", "chat-pin:i0", "idqCanonicalBotLongValue")

	if len(got.Warnings) == 0 {
		t.Fatal("expected warnings for missing proof metadata")
	}
	if !containsWarning(got.Warnings, "txid/contentHash metadata is missing") {
		t.Fatalf("warnings = %v, want missing metadata warning", got.Warnings)
	}

	if got.Source.Resolver != "metaso-p2p" {
		t.Fatalf("Source.Resolver = %q", got.Source.Resolver)
	}
	if got.Source.Node != "https://file.metaid.io" {
		t.Fatalf("Source.Node = %q", got.Source.Node)
	}
	if got.Source.ProfileEndpoint != "/api/info/globalmetaid/:globalMetaId" {
		t.Fatalf("Source.ProfileEndpoint = %q", got.Source.ProfileEndpoint)
	}
	if got.Source.ServiceEndpoint != "/api/bot-hub/skill-service/list" {
		t.Fatalf("Source.ServiceEndpoint = %q", got.Source.ServiceEndpoint)
	}
	if got.Source.ContentBaseURL != "https://file.metaid.io/metafile-indexer/content" {
		t.Fatalf("Source.ContentBaseURL = %q", got.Source.ContentBaseURL)
	}
	if got.Source.FetchedAt != 1780760000000 {
		t.Fatalf("Source.FetchedAt = %d", got.Source.FetchedAt)
	}
	if got.Source.Stale {
		t.Fatal("Source.Stale = true, want false")
	}
}

func TestBuildHomepageIncludesProviderServices(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.now = func() int64 { return 1780760000000 }
	agg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:    "idqBot",
		MetaId:          "metaBot",
		Address:         "1BotAddress",
		Name:            "IDQ Bot",
		ChatPublicKey:   "02chatpubkey",
		ChatPublicKeyId: "chat-pin:i0",
		ChainName:       "mvc",
	}})

	lister := &recordingServiceLister{result: &skillservice.ListResult{List: []skillservice.ServiceListItem{{
		Id:                 "svc-1",
		CurrentPinId:       "svc-pin:i0",
		SourceServicePinId: "source-svc-pin:i0",
		DisplayName:        "Question Answering",
		ServiceName:        "qa",
		Description:        "Answers questions from chain context.",
		ServiceIcon:        "metafile://service-icon",
		ProviderSkill:      "qa-skill",
		OutputType:         "text",
		Price:              "0.1",
		Currency:           "SPACE",
		SettlementKind:     "prepaid",
		PaymentChain:       "mvc",
		MRC20Ticker:        "SPACE",
		MRC20Id:            "space-token-id",
		PaymentAddress:     "1PayAddress",
		RatingAvg:          4.7,
		RatingCount:        12,
		Status:             1,
		Operation:          "create",
		Disabled:           false,
		ChainName:          "mvc",
		CreatedAt:          1780750000000,
		UpdatedAt:          1780755000000,
	}}}}
	agg.SetServiceLister(lister)

	opts := DefaultOptions()
	opts.ServiceSize = 7
	opts.IncludeInactiveServices = true
	opts.ChainName = "mvc"

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if lister.gotParams.ProviderGlobalMetaId != "idqBot" {
		t.Fatalf("ProviderGlobalMetaId = %q, want idqBot", lister.gotParams.ProviderGlobalMetaId)
	}
	if lister.gotParams.Size != 7 {
		t.Fatalf("Size = %d, want 7", lister.gotParams.Size)
	}
	if !lister.gotParams.IncludeInactive {
		t.Fatal("IncludeInactive = false, want true")
	}
	if lister.gotParams.ChainName != "mvc" {
		t.Fatalf("ChainName = %q, want mvc", lister.gotParams.ChainName)
	}
	if lister.gotParams.SortBy != "updated" {
		t.Fatalf("SortBy = %q, want updated", lister.gotParams.SortBy)
	}
	if lister.gotParams.Order != "desc" {
		t.Fatalf("Order = %q, want desc", lister.gotParams.Order)
	}

	if len(got.Services) != 1 {
		t.Fatalf("Services length = %d, want 1", len(got.Services))
	}
	service := got.Services[0]
	if service.Id != "svc-1" || service.DisplayName != "Question Answering" || service.ProviderSkill != "qa-skill" {
		t.Fatalf("service mapping = %+v", service)
	}
	if service.Proof == nil {
		t.Fatal("service.Proof = nil, want proof")
	}
	if service.Proof.ProtocolPath != skillservice.PathSkillService {
		t.Fatalf("service proof ProtocolPath = %q, want %q", service.Proof.ProtocolPath, skillservice.PathSkillService)
	}
	if !got.Actions[1].Enabled {
		t.Fatalf("services action = %+v, want enabled", got.Actions[1])
	}
	if len(got.Proofs.Services) != 1 {
		t.Fatalf("Proofs.Services length = %d, want 1", len(got.Proofs.Services))
	}
}

func TestBuildHomepageServiceProofsMarkVerificationPartial(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:  "idqBot",
		Name:          "IDQ Bot",
		ChatPublicKey: "02chatpubkey",
	}})
	agg.SetServiceLister(&recordingServiceLister{result: &skillservice.ListResult{List: []skillservice.ServiceListItem{{
		Id:                 "svc-1",
		CurrentPinId:       "svc-pin:i0",
		SourceServicePinId: "source-svc-pin:i0",
		DisplayName:        "Question Answering",
		ServiceName:        "qa",
	}}}})

	got, err := agg.Build("idqBot", DefaultOptions())
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Proofs.VerificationState != "partial" {
		t.Fatalf("Proofs.VerificationState = %q, want partial", got.Proofs.VerificationState)
	}
	if len(got.Proofs.Services) != 1 {
		t.Fatalf("Proofs.Services length = %d, want 1", len(got.Proofs.Services))
	}
	if !containsWarning(got.Warnings, "txid/contentHash") {
		t.Fatalf("Warnings = %v, want missing txid/contentHash warning", got.Warnings)
	}
}

func TestBuildHomepageServiceListerErrorReturnsAggregationUnavailable(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "IDQ Bot",
	}})
	agg.SetServiceLister(&recordingServiceLister{err: errors.New("list unavailable")})

	_, err := agg.Build("idqBot", DefaultOptions())
	if !errors.Is(err, ErrAggregationUnavailable) {
		t.Fatalf("Build error = %v, want ErrAggregationUnavailable", err)
	}
}

func TestBuildHomepageSkipsServicesWhenDisabled(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:  "idqBot",
		Name:          "IDQ Bot",
		ChatPublicKey: "02chatpubkey",
	}})
	lister := &recordingServiceLister{result: &skillservice.ListResult{List: []skillservice.ServiceListItem{{
		Id: "svc-1",
	}}}}
	agg.SetServiceLister(lister)

	opts := DefaultOptions()
	opts.IncludeServices = false
	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if lister.gotParams != (skillservice.ListParams{}) {
		t.Fatalf("service lister params = %+v, want zero value", lister.gotParams)
	}
	if len(got.Services) != 0 {
		t.Fatalf("Services length = %d, want 0", len(got.Services))
	}
}

func TestBuildHomepageSuppressesProofsWhenDisabled(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:    "idqBot",
		Name:            "IDQ Bot",
		NameId:          "name-pin:i0",
		ChatPublicKey:   "02chatpubkey",
		ChatPublicKeyId: "chat-pin:i0",
	}})
	lister := &recordingServiceLister{result: &skillservice.ListResult{List: []skillservice.ServiceListItem{{
		Id:                 "svc-1",
		CurrentPinId:       "svc-pin:i0",
		SourceServicePinId: "source-svc-pin:i0",
		DisplayName:        "Question Answering",
		ServiceName:        "qa",
	}}}}
	agg.SetServiceLister(lister)

	opts := DefaultOptions()
	opts.IncludeProofs = false
	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if len(got.Proofs.Profile) != 0 {
		t.Fatalf("Proofs.Profile length = %d, want 0", len(got.Proofs.Profile))
	}
	if len(got.Proofs.Services) != 0 {
		t.Fatalf("Proofs.Services length = %d, want 0", len(got.Proofs.Services))
	}
	if len(got.Services) != 1 {
		t.Fatalf("Services length = %d, want 1", len(got.Services))
	}
	if got.Services[0].Proof != nil {
		t.Fatalf("service Proof = %+v, want nil", got.Services[0].Proof)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want empty", got.Warnings)
	}
}

func TestCustomHomepagePlanSchema(t *testing.T) {
	custom := CustomHomepage{
		URI:          "metafile://homepage-pin",
		PinId:        "homepage-pin:i0",
		ContentType:  "text/html",
		Renderer:     "html",
		Txid:         "homepage-tx",
		ProtocolPath: "/protocols/bot-homepage",
	}
	if custom.URI == "" || custom.PinId == "" || custom.ContentType == "" || custom.Renderer == "" || custom.Txid == "" || custom.ProtocolPath == "" {
		t.Fatalf("custom homepage schema fields did not round trip: %+v", custom)
	}
}

func TestProfileStableJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Profile{})
	if err != nil {
		t.Fatalf("json.Marshal(Profile{}): %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", raw, err)
	}

	for _, key := range []string{"avatarPinId", "backgroundPinId", "bioPinId", "chatPubkeyPinId", "nftAvatar"} {
		value, ok := got[key]
		if !ok {
			t.Fatalf("zero-value Profile JSON missing stable key %q: %s", key, raw)
		}
		if value != "" {
			t.Fatalf("zero-value Profile JSON key %q = %v, want empty string", key, value)
		}
	}
}

func assertProfileProof(t *testing.T, proofs []ProfileProof, field, path, pinID, publisher string) {
	t.Helper()
	for _, proof := range proofs {
		if proof.Field == field {
			if proof.ProtocolPath != path {
				t.Fatalf("%s ProtocolPath = %q, want %q", field, proof.ProtocolPath, path)
			}
			if proof.PinId != pinID {
				t.Fatalf("%s PinId = %q, want %q", field, proof.PinId, pinID)
			}
			if proof.PublisherGlobalMetaId != publisher {
				t.Fatalf("%s PublisherGlobalMetaId = %q, want %q", field, proof.PublisherGlobalMetaId, publisher)
			}
			return
		}
	}
	t.Fatalf("missing profile proof for field %q: %+v", field, proofs)
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
