package bothomepage

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
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

type recordingHomepageServiceLister struct {
	gotParams skillservice.HomepageListParams
	result    *skillservice.HomepageListResult
	err       error
}

func (r *recordingHomepageServiceLister) ListHomepageByProvider(params skillservice.HomepageListParams) (*skillservice.HomepageListResult, error) {
	r.gotParams = params
	return r.result, r.err
}

type recordingPublishedContentLister struct {
	gotParams []publishedcontent.ListParams
	result    *publishedcontent.ListResult
	err       error
}

func (r *recordingPublishedContentLister) List(params publishedcontent.ListParams) (*publishedcontent.ListResult, error) {
	r.gotParams = append(r.gotParams, params)
	return r.result, r.err
}

type pathPublishedContentLister struct {
	gotParams []publishedcontent.ListParams
}

func (p *pathPublishedContentLister) List(params publishedcontent.ListParams) (*publishedcontent.ListResult, error) {
	p.gotParams = append(p.gotParams, params)
	return &publishedcontent.ListResult{Items: []publishedcontent.SectionItem{{
		SourcePinId:           params.ProtocolPath + ":source",
		CurrentPinId:          params.ProtocolPath + ":current",
		ProtocolPath:          params.ProtocolPath,
		PublisherGlobalMetaId: params.PublisherGlobalMetaId,
		ChainName:             params.ChainName,
		PayloadText:           params.ProtocolPath + " payload",
		PayloadExposed:        true,
		ContentType:           "text/plain",
		CurrentNumber:         1,
		CurrentHost:           "mvc",
		CurrentPath:           params.ProtocolPath,
		PublisherMetaId:       "metaBot",
		PublisherAddress:      "1BotAddress",
		SourceNumber:          1,
		SourcePath:            params.ProtocolPath,
		SourceHost:            "mvc",
	}}}, nil
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

func TestBuildV2ParsesLegacyBioIntoPersona(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Persona Bot",
		Bio:          `{"role":"Agent role","soul":"Warm","goal":"Help","llm":"deepseek","allowChatSkills":["metabot-post-buzz"]}`,
		BioId:        "bio:i0",
	}})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true
	opts.IncludeProofs = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.SchemaVersion != "botHomepage.v2" {
		t.Fatalf("SchemaVersion = %q, want botHomepage.v2", got.SchemaVersion)
	}
	if got.Profile.Bio != "" {
		t.Fatalf("Profile.Bio = %q, want empty for legacy persona JSON", got.Profile.Bio)
	}
	if got.Persona == nil {
		t.Fatal("Persona = nil, want parsed persona")
	}
	if got.Persona.Role != "Agent role" {
		t.Fatalf("Persona.Role = %q, want Agent role", got.Persona.Role)
	}
	if got.Persona.Soul != "Warm" {
		t.Fatalf("Persona.Soul = %q, want Warm", got.Persona.Soul)
	}
	if got.Persona.Goal != "Help" {
		t.Fatalf("Persona.Goal = %q, want Help", got.Persona.Goal)
	}
	if got.Persona.LLM.Provider != "deepseek" {
		t.Fatalf("Persona.LLM.Provider = %q, want deepseek", got.Persona.LLM.Provider)
	}
	if len(got.Persona.ChatSkills.Allow) != 1 || got.Persona.ChatSkills.Allow[0] != "metabot-post-buzz" {
		t.Fatalf("Persona.ChatSkills.Allow = %#v, want metabot-post-buzz", got.Persona.ChatSkills.Allow)
	}
	if got.Homepage.Summary != "Agent role" {
		t.Fatalf("Homepage.Summary = %q, want Agent role", got.Homepage.Summary)
	}
}

func TestProfileFromUserInfoCarriesHomepage(t *testing.T) {
	profile := profileFromUserInfo(&userinfo.UserProfile{
		GlobalMetaID: "idqBot",
		Homepage:     `{"uri":"metafile://homepage-pin","renderer":"html"}`,
		HomepageId:   "homepage-pin:i0",
	})

	if profile.Homepage != `{"uri":"metafile://homepage-pin","renderer":"html"}` {
		t.Fatalf("Homepage = %q, want stored homepage payload", profile.Homepage)
	}
	if profile.HomepageId != "homepage-pin:i0" {
		t.Fatalf("HomepageId = %q, want homepage-pin:i0", profile.HomepageId)
	}
}

func TestBuildV2UsesCustomHomepageFromUserInfo(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Homepage Bot",
		Bio:          `{"role":"legacy role","goal":"legacy goal"}`,
		Role:         "Live role",
		RoleId:       "role-pin:i0",
		Homepage:     `{"uri":"metafile://homepage-pin","contentType":"text/html","renderer":"html","txid":"homepage-tx"}`,
		HomepageId:   "homepage-pin:i0",
	}})

	opts := DefaultOptions()
	opts.Version = "v2"

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Homepage.Mode != "custom" {
		t.Fatalf("Homepage.Mode = %q, want custom", got.Homepage.Mode)
	}
	if got.Homepage.Title != "Homepage Bot" {
		t.Fatalf("Homepage.Title = %q, want Homepage Bot", got.Homepage.Title)
	}
	if got.Homepage.Summary != "Live role" {
		t.Fatalf("Homepage.Summary = %q, want Live role without legacy JSON leak", got.Homepage.Summary)
	}
	if got.Homepage.Custom == nil {
		t.Fatal("Homepage.Custom = nil, want custom homepage")
	}
	if got.Homepage.Custom.URI != "metafile://homepage-pin" {
		t.Fatalf("Homepage.Custom.URI = %q, want metafile://homepage-pin", got.Homepage.Custom.URI)
	}
	if got.Homepage.Custom.PinId != "homepage-pin:i0" {
		t.Fatalf("Homepage.Custom.PinId = %q, want homepage-pin:i0", got.Homepage.Custom.PinId)
	}
	if got.Homepage.Custom.ContentType != "text/html" {
		t.Fatalf("Homepage.Custom.ContentType = %q, want text/html", got.Homepage.Custom.ContentType)
	}
	if got.Homepage.Custom.Renderer != "html" {
		t.Fatalf("Homepage.Custom.Renderer = %q, want html", got.Homepage.Custom.Renderer)
	}
	if got.Homepage.Custom.Txid != "homepage-tx" {
		t.Fatalf("Homepage.Custom.Txid = %q, want homepage-tx", got.Homepage.Custom.Txid)
	}
	if got.Homepage.Custom.ProtocolPath != "/info/homepage" {
		t.Fatalf("Homepage.Custom.ProtocolPath = %q, want /info/homepage", got.Homepage.Custom.ProtocolPath)
	}
}

func TestBuildV2WithoutHomepageUsesDefaultHomepage(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Default Bot",
		Goal:         "Help people",
	}})

	opts := DefaultOptions()
	opts.Version = "v2"

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Homepage.Mode != "default" {
		t.Fatalf("Homepage.Mode = %q, want default", got.Homepage.Mode)
	}
	if got.Homepage.Custom != nil {
		t.Fatalf("Homepage.Custom = %+v, want nil", got.Homepage.Custom)
	}
	if got.Homepage.Summary != "Help people" {
		t.Fatalf("Homepage.Summary = %q, want persona goal fallback", got.Homepage.Summary)
	}
}

func TestBuildV2ParsesMixedShapeLegacyBioIntoPersona(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Persona Bot",
		Bio:          `{"role":"Agent role","tools":["metabot-post-buzz"],"llm":{"primaryProvider":"deepseek","displayName":"DeepSeek"}}`,
	}})

	opts := DefaultOptions()
	opts.Version = "v2"

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Profile.Bio != "" {
		t.Fatalf("Profile.Bio = %q, want empty for mixed-shape legacy persona JSON", got.Profile.Bio)
	}
	if got.Persona == nil {
		t.Fatal("Persona = nil, want parsed persona")
	}
	if got.Persona.Role != "Agent role" {
		t.Fatalf("Persona.Role = %q, want Agent role", got.Persona.Role)
	}
	if len(got.Persona.ChatSkills.Allow) != 1 || got.Persona.ChatSkills.Allow[0] != "metabot-post-buzz" {
		t.Fatalf("Persona.ChatSkills.Allow = %#v, want metabot-post-buzz", got.Persona.ChatSkills.Allow)
	}
	if got.Persona.LLM.Provider != "deepseek" {
		t.Fatalf("Persona.LLM.Provider = %q, want deepseek", got.Persona.LLM.Provider)
	}
}

func TestBuildV2IgnoresMalformedPreferredPersonaJSON(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Persona Bot",
		ChatSkills:   "{bad",
		LLM:          "{bad",
	}})

	opts := DefaultOptions()
	opts.Version = "v2"

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Persona == nil {
		t.Fatal("Persona = nil, want empty persona")
	}
	if len(got.Persona.ChatSkills.Allow) != 0 {
		t.Fatalf("Persona.ChatSkills.Allow = %#v, want empty for malformed preferred JSON", got.Persona.ChatSkills.Allow)
	}
	if got.Persona.LLM.Provider != "" {
		t.Fatalf("Persona.LLM.Provider = %q, want empty for malformed preferred JSON", got.Persona.LLM.Provider)
	}
}

func TestBuildV2SectionsAreOptional(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Section Bot",
	}})
	agg.SetPublishedContentLister(&recordingPublishedContentLister{err: errors.New("publishedcontent unavailable")})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.SchemaVersion != "botHomepage.v2" {
		t.Fatalf("SchemaVersion = %q, want botHomepage.v2", got.SchemaVersion)
	}
	if len(got.Sections) != 4 {
		t.Fatalf("Sections length = %d, want 4; sections=%#v", len(got.Sections), got.Sections)
	}
	metaapps := got.Sections[1]
	if metaapps.Id != "metaapps" || metaapps.Title != "MetaAPPs" || metaapps.Kind != "metaapps" {
		t.Fatalf("metaapps section identity = %+v", metaapps)
	}
	if len(metaapps.Items) != 0 || metaapps.Limit != 5 || metaapps.Returned != 0 || metaapps.HasMore {
		t.Fatalf("metaapps section paging = %+v", metaapps)
	}
	if metaapps.More.Label != "More" || metaapps.More.Enabled {
		t.Fatalf("metaapps more = %+v, want disabled More", metaapps.More)
	}
	if !containsExactWarning(got.Warnings, "metaapps section unavailable") {
		t.Fatalf("Warnings = %#v, want exact metaapps section unavailable", got.Warnings)
	}
}

func TestBuildV2SkipsServicesSectionWhenIncludeServicesFalse(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Section Bot",
	}})
	agg.SetHomepageServiceLister(&recordingHomepageServiceLister{result: &skillservice.HomepageListResult{List: []skillservice.ServiceListItem{{
		Id:          "svc-1",
		DisplayName: "Question Answering",
	}}}})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true
	opts.IncludeServices = false

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if len(got.Services) != 0 {
		t.Fatalf("Services length = %d, want 0 when includeServices=false", len(got.Services))
	}
	for _, section := range got.Sections {
		if section.Id == "services" {
			t.Fatalf("sections = %+v, want services section omitted when includeServices=false", got.Sections)
		}
	}
	if got.Actions[1].Enabled {
		t.Fatalf("services action = %+v, want disabled when services are excluded", got.Actions[1])
	}
}

func TestBuildV2SectionTogglesDisablePublishedSections(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Toggle Bot",
	}})
	lister := &recordingPublishedContentLister{result: &publishedcontent.ListResult{Items: []publishedcontent.SectionItem{{
		CurrentPinId:   "buzz-pin:i0",
		ProtocolPath:   publishedcontent.PathSimpleBuzz,
		PayloadText:    "buzz",
		PayloadExposed: true,
	}}}}
	agg.SetPublishedContentLister(lister)

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true
	opts.IncludeMetaApps = false
	opts.IncludeSkills = false
	opts.IncludeBuzzes = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if len(got.Sections) != 2 {
		t.Fatalf("Sections length = %d, want services plus buzzes; sections=%+v", len(got.Sections), got.Sections)
	}
	if got.Sections[0].Id != "services" || got.Sections[1].Id != "buzzes" {
		t.Fatalf("section ids = %q/%q, want services/buzzes", got.Sections[0].Id, got.Sections[1].Id)
	}
	if len(lister.gotParams) != 1 {
		t.Fatalf("published content list calls = %d, want 1", len(lister.gotParams))
	}
	if lister.gotParams[0].ProtocolPath != publishedcontent.PathSimpleBuzz {
		t.Fatalf("ProtocolPath = %q, want %q", lister.gotParams[0].ProtocolPath, publishedcontent.PathSimpleBuzz)
	}
}

func TestBuildV2UsesFixedServiceSize(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Fixed Size Bot",
	}})
	lister := &recordingServiceLister{result: &skillservice.ListResult{}}
	agg.SetServiceLister(lister)

	opts, err := ParseOptions(url.Values{"version": {"v2"}, "serviceSize": {"1"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}

	if _, err := agg.Build("idqBot", opts); err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if lister.gotParams.Size != homepageSectionLimit {
		t.Fatalf("service lister Size = %d, want fixed v2 homepage limit %d", lister.gotParams.Size, homepageSectionLimit)
	}
}

func TestBuildV2SectionsExposeMempoolContentItems(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Section Bot",
	}})
	agg.SetPublishedContentLister(&recordingPublishedContentLister{result: &publishedcontent.ListResult{Items: []publishedcontent.SectionItem{{
		SourcePinId:  "source-pin:i0",
		CurrentPinId: "current-pin:i0",
		ProtocolPath: publishedcontent.PathMetaApp,
		IsMempool:    true,
		PayloadText:  "pending metaapp",
	}}}})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	metaapps := got.Sections[1]
	if metaapps.Id != "metaapps" || len(metaapps.Items) != 1 {
		t.Fatalf("metaapps section = %+v, want one content item", metaapps)
	}
	if !metaapps.Items[0].IsMempool {
		t.Fatalf("metaapps item IsMempool = false, want true; item=%+v", metaapps.Items[0])
	}
}

func TestBuildV2SectionsExposePayloadUnderData(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Payload Bot",
	}})
	agg.SetPublishedContentLister(&recordingPublishedContentLister{result: &publishedcontent.ListResult{Items: []publishedcontent.SectionItem{
		{
			SourcePinId:  "json-source:i0",
			CurrentPinId: "json-current:i0",
			ProtocolPath: publishedcontent.PathMetaApp,
			ContentType:  "application/json",
			PayloadJSON: map[string]any{
				"title": "JSON MetaAPP",
				"kind":  "tool",
			},
			PayloadExposed: true,
		},
		{
			SourcePinId:     "text-source:i0",
			CurrentPinId:    "text-current:i0",
			ProtocolPath:    publishedcontent.PathMetaApp,
			ContentType:     "text/plain",
			PayloadText:     "plain homepage item",
			PayloadExposed:  true,
			IsMempool:       true,
			PublisherMetaId: "metaBot",
		},
	}}})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	metaapps := got.Sections[1]
	if len(metaapps.Items) != 2 {
		t.Fatalf("metaapps items = %d, want 2; section=%+v", len(metaapps.Items), metaapps)
	}

	jsonPayload, ok := metaapps.Items[0].Data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("JSON item data.payload = %#v, want object", metaapps.Items[0].Data["payload"])
	}
	if jsonPayload["title"] != "JSON MetaAPP" || jsonPayload["kind"] != "tool" {
		t.Fatalf("JSON payload = %#v, want structured payload", jsonPayload)
	}
	textPayload, ok := metaapps.Items[1].Data["payload"].(string)
	if !ok || textPayload != "plain homepage item" {
		t.Fatalf("text item data.payload = %#v, want plain homepage item", metaapps.Items[1].Data["payload"])
	}

	raw, err := json.Marshal(metaapps.Items[0])
	if err != nil {
		t.Fatalf("json.Marshal section item: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("json.Unmarshal section item %s: %v", raw, err)
	}
	data, ok := encoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("encoded data = %#v, want object; raw=%s", encoded["data"], raw)
	}
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("encoded data.payload = %#v, want object; raw=%s", data["payload"], raw)
	}
	if payload["title"] != "JSON MetaAPP" {
		t.Fatalf("encoded data.payload.title = %#v, want JSON MetaAPP; raw=%s", payload["title"], raw)
	}
}

func TestSectionWithItemsKeepsMoreDisabledWhenHasMore(t *testing.T) {
	items := []SectionItem{
		{Id: "item-1"},
		{Id: "item-2"},
		{Id: "item-3"},
		{Id: "item-4"},
		{Id: "item-5"},
		{Id: "item-6"},
	}

	got := sectionWithItems("buzzes", "Buzzes", "buzzes", items, false)

	if !got.HasMore {
		t.Fatal("HasMore = false, want true when input exceeds section limit")
	}
	if got.Returned != homepageSectionLimit || len(got.Items) != homepageSectionLimit {
		t.Fatalf("returned items = %d/%d, want section limit %d", got.Returned, len(got.Items), homepageSectionLimit)
	}
	if got.More.Label != "More" || got.More.Enabled {
		t.Fatalf("More = %+v, want disabled More label", got.More)
	}
}

func TestSectionWithItemsKeepsMoreDisabledForListerHasMore(t *testing.T) {
	got := sectionWithItems("services", "Services", "services", []SectionItem{{Id: "svc-1"}}, true)

	if !got.HasMore {
		t.Fatal("HasMore = false, want true from lister result")
	}
	if got.More.Label != "More" || got.More.Enabled {
		t.Fatalf("More = %+v, want disabled More label", got.More)
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

func TestBuildV2ProofsSeparatePersonaHomepageAndSections(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:  "idqBot",
		Name:          "Proof Bot",
		NameId:        "name-pin:i0",
		Bio:           "Proof summary",
		BioId:         "bio-pin:i0",
		Role:          "Research agent",
		RoleId:        "role-pin:i0",
		Soul:          "Patient",
		SoulId:        "soul-pin:i0",
		Goal:          "Explain proofs",
		GoalId:        "goal-pin:i0",
		ChatSkills:    `["metabot-post-buzz"]`,
		ChatSkillsId:  "skills-pin:i0",
		LLM:           `{"provider":"deepseek","model":"v3"}`,
		LLMId:         "llm-pin:i0",
		Homepage:      `{"uri":"metafile://homepage","renderer":"html"}`,
		HomepageId:    "homepage-pin:i0",
		ChatPublicKey: "02chat",
	}})
	agg.SetHomepageServiceLister(&recordingHomepageServiceLister{result: &skillservice.HomepageListResult{List: []skillservice.ServiceListItem{{
		Id:                 "svc-1",
		CurrentPinId:       "svc-pin:i0",
		SourceServicePinId: "source-svc-pin:i0",
		DisplayName:        "Proof Service",
	}}}})
	agg.SetPublishedContentLister(&pathPublishedContentLister{})

	opts := DefaultOptions()
	opts.Version = "v2"
	opts.IncludeSections = true
	opts.IncludeProofs = true

	got, err := agg.Build("idqBot", opts)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Proofs.VerificationState != "partial" {
		t.Fatalf("Proofs.VerificationState = %q, want partial", got.Proofs.VerificationState)
	}
	assertProfileProof(t, got.Proofs.Profile, "name", "/info/name", "name-pin:i0", "idqBot")
	assertProfileProof(t, got.Proofs.Profile, "bio", "/info/bio", "bio-pin:i0", "idqBot")
	for _, field := range []string{"role", "soul", "goal", "chatSkills", "llm"} {
		if !hasProfileProofField(got.Proofs.Persona, field) {
			t.Fatalf("missing persona proof for %q: %+v", field, got.Proofs.Persona)
		}
	}
	assertProfileProof(t, got.Proofs.Persona, "role", "/info/role", "role-pin:i0", "idqBot")
	assertProfileProof(t, got.Proofs.Persona, "soul", "/info/soul", "soul-pin:i0", "idqBot")
	assertProfileProof(t, got.Proofs.Persona, "goal", "/info/goal", "goal-pin:i0", "idqBot")
	assertProfileProof(t, got.Proofs.Persona, "chatSkills", "/info/chatSkills", "skills-pin:i0", "idqBot")
	assertProfileProof(t, got.Proofs.Persona, "llm", "/info/LLM", "llm-pin:i0", "idqBot")
	if got.Proofs.Persona[0].Txid != "" || got.Proofs.Persona[0].ContentHash != "" {
		t.Fatalf("persona proof should not fake txid/contentHash: %+v", got.Proofs.Persona[0])
	}

	if got.Proofs.Homepage == nil {
		t.Fatal("Proofs.Homepage = nil, want /info/homepage proof")
	}
	if got.Proofs.Homepage.PinId != "homepage-pin:i0" || got.Proofs.Homepage.ProtocolPath != "/info/homepage" {
		t.Fatalf("Proofs.Homepage = %+v, want /info/homepage pin", got.Proofs.Homepage)
	}
	if got.Proofs.Homepage.Txid != "" || got.Proofs.Homepage.ContentHash != "" || got.Proofs.Homepage.ExplorerURL != "" {
		t.Fatalf("homepage proof should not fake txid/contentHash/explorer: %+v", got.Proofs.Homepage)
	}

	for _, sectionID := range []string{"services", "metaapps", "skills", "buzzes"} {
		proofs := got.Proofs.Sections[sectionID]
		if len(proofs) != 1 {
			t.Fatalf("Proofs.Sections[%q] length = %d, want 1; proofs=%+v", sectionID, len(proofs), got.Proofs.Sections)
		}
		if proofs[0].PinId == "" || proofs[0].ProtocolPath == "" {
			t.Fatalf("Proofs.Sections[%q][0] = %+v, want pinId and protocolPath", sectionID, proofs[0])
		}
		if proofs[0].Txid != "" || proofs[0].ContentHash != "" || proofs[0].ExplorerURL != "" {
			t.Fatalf("section proof should not fake txid/contentHash/explorer: %+v", proofs[0])
		}
	}

	for _, section := range got.Sections {
		if len(section.Items) == 0 {
			continue
		}
		if section.Items[0].Proof == nil {
			t.Fatalf("section %q first item proof = nil, want renderable proof summary", section.Id)
		}
		if section.Items[0].Proof.PinId == "" || section.Items[0].Proof.ProtocolPath == "" {
			t.Fatalf("section %q first item proof = %+v, want pinId and protocolPath", section.Id, section.Items[0].Proof)
		}
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

func hasProfileProofField(proofs []ProfileProof, field string) bool {
	for _, proof := range proofs {
		if proof.Field == field {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func containsExactWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
