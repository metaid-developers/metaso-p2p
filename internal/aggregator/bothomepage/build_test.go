package bothomepage

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
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

func defaultV3Options() Options {
	opts := DefaultOptions()
	opts.Version = "v3"
	opts.IncludePresence = true
	opts.IncludeSections = true
	opts.IncludeServices = true
	opts.IncludeBuzzes = true
	opts.IncludeMetaApps = true
	opts.IncludeSkills = false
	opts.IncludeProofs = false
	opts.ServiceSize = homepageSectionLimit
	opts.ChainName = ""
	return opts
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

type protocolRecordingPublishedContentLister struct {
	gotParams []publishedcontent.ListParams
	results   map[string]*publishedcontent.ListResult
	errs      map[string]error
}

func (r *protocolRecordingPublishedContentLister) List(params publishedcontent.ListParams) (*publishedcontent.ListResult, error) {
	r.gotParams = append(r.gotParams, params)
	if err := r.errs[params.ProtocolPath]; err != nil {
		return nil, err
	}
	if result := r.results[params.ProtocolPath]; result != nil {
		return result, nil
	}
	return &publishedcontent.ListResult{}, nil
}

type identityPublishedContentLister struct {
	gotParams []publishedcontent.ListParams
}

func (l *identityPublishedContentLister) List(params publishedcontent.ListParams) (*publishedcontent.ListResult, error) {
	l.gotParams = append(l.gotParams, params)
	if params.PublisherAddress == "1BotAddress" {
		return &publishedcontent.ListResult{Items: []publishedcontent.SectionItem{{
			SourcePinId:      "address-buzz:i0",
			CurrentPinId:     "address-buzz:i0",
			ProtocolPath:     publishedcontent.PathSimpleBuzz,
			PublisherAddress: "1BotAddress",
			ContentType:      "text/plain",
			PayloadText:      "address indexed buzz",
			PayloadExposed:   true,
			CreatedAt:        1781252638,
		}}}, nil
	}
	return &publishedcontent.ListResult{}, nil
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
		Homepage:     `{"uri":"metafile://homepage-pin","contentType":"text/html","renderer":"html","theme":"dark","permissions":["chat"]}`,
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
	var custom map[string]any
	if err := json.Unmarshal(*got.Homepage.Custom, &custom); err != nil {
		t.Fatalf("Homepage.Custom is not a JSON object: %v", err)
	}
	if custom["uri"] != "metafile://homepage-pin" {
		t.Fatalf("custom uri = %#v, want metafile://homepage-pin", custom["uri"])
	}
	if custom["renderer"] != "html" {
		t.Fatalf("custom renderer = %#v, want html", custom["renderer"])
	}
	if custom["theme"] != "dark" {
		t.Fatalf("custom theme = %#v, want dark", custom["theme"])
	}
	permissions, ok := custom["permissions"].([]any)
	if !ok || len(permissions) != 1 || permissions[0] != "chat" {
		t.Fatalf("custom permissions = %#v, want [chat]", custom["permissions"])
	}
	if _, ok := custom["pinId"]; ok {
		t.Fatalf("custom unexpectedly contains server pinId: %#v", custom["pinId"])
	}
	if _, ok := custom["protocolPath"]; ok {
		t.Fatalf("custom unexpectedly contains server protocolPath: %#v", custom["protocolPath"])
	}
}

func TestBuildV2IgnoresNonJSONHomepageValue(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Plain Homepage Bot",
		Homepage:     "metafile://homepage-pin",
		HomepageId:   "homepage-pin:i0",
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
		t.Fatalf("Homepage.Custom = %s, want nil", string(*got.Homepage.Custom))
	}
}

func TestBuildV2IgnoresEmptyJSONHomepageObject(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "Empty Homepage Bot",
		Homepage:     `{}`,
		HomepageId:   "homepage-pin:i0",
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
		t.Fatalf("Homepage.Custom = %s, want nil", string(*got.Homepage.Custom))
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

func TestBuildV2PublishedSectionsUseCanonicalAddressFallback(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		MetaId:       "metaBot",
		Address:      "1BotAddress",
		Name:         "Address Bot",
	}})
	lister := &identityPublishedContentLister{}
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

	if len(lister.gotParams) != 3 {
		t.Fatalf("published content calls = %d, want global, metaid, then address fallback; params=%+v", len(lister.gotParams), lister.gotParams)
	}
	if lister.gotParams[0].PublisherGlobalMetaId != "idqBot" {
		t.Fatalf("first query globalMetaId = %q, want idqBot", lister.gotParams[0].PublisherGlobalMetaId)
	}
	if lister.gotParams[1].PublisherMetaId != "metaBot" {
		t.Fatalf("second query metaid = %q, want metaBot", lister.gotParams[1].PublisherMetaId)
	}
	if lister.gotParams[2].PublisherAddress != "1BotAddress" {
		t.Fatalf("fallback query address = %q, want 1BotAddress", lister.gotParams[2].PublisherAddress)
	}

	buzzes := got.Sections[1]
	if buzzes.Id != "buzzes" || len(buzzes.Items) != 1 {
		t.Fatalf("buzzes section = %+v, want one address-indexed item", buzzes)
	}
	if buzzes.Items[0].SourcePinId != "address-buzz:i0" {
		t.Fatalf("buzz source pin = %q, want address-buzz:i0", buzzes.Items[0].SourcePinId)
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

func TestBuildV3ProfileUsesRawBotInfoBlocks(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	lookup := &fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:      "idqCanonicalBotLongValue",
		MetaId:            "metaBot",
		Name:              "Fortune Bot",
		NameId:            "name-pin:i0",
		Bio:               "Reads the chain and answers directly.",
		BioId:             "bio-pin:i0",
		ChatPublicKey:     "02chatpubkey",
		ChatPublicKeyId:   "chat-pin:i0",
		AvatarId:          "avatar-pin:i0",
		AvatarContentType: "image/png;binary",
		LLM:               `{"provider":"openai","model":"gpt-4.1"}`,
		LLMId:             "llm-pin:i0",
		Persona:           `{"style":"direct","language":"zh-CN"}`,
		PersonaId:         "persona-pin:i0",
		Homepage:          `{"uri":"metaapp://homepage","renderer":"metaapp"}`,
		HomepageId:        "homepage-pin:i0",
	}}
	agg.SetProfileLookup(lookup)

	got, err := agg.BuildV3("idqRequestedBot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}

	if lookup.seen != "idqRequestedBot" {
		t.Fatalf("lookup globalMetaId = %q, want idqRequestedBot", lookup.seen)
	}
	if got.SchemaVersion != schemaVersionV3 {
		t.Fatalf("SchemaVersion = %q, want %q", got.SchemaVersion, schemaVersionV3)
	}
	if got.Identity.GlobalMetaId != "idqCanonicalBotLongValue" {
		t.Fatalf("Identity.GlobalMetaId = %q", got.Identity.GlobalMetaId)
	}
	if got.Identity.LegacyMetaId != "metaBot" {
		t.Fatalf("Identity.LegacyMetaId = %q", got.Identity.LegacyMetaId)
	}
	if got.Identity.Display == "" || got.Identity.Display == got.Identity.GlobalMetaId {
		t.Fatalf("Identity.Display = %q, want abbreviated canonical id", got.Identity.Display)
	}
	if got.Profile.Name != "Fortune Bot" {
		t.Fatalf("Profile.Name = %q", got.Profile.Name)
	}
	if got.Profile.Bio != "Reads the chain and answers directly." {
		t.Fatalf("Profile.Bio = %q", got.Profile.Bio)
	}
	if got.Profile.ChatPubkey != "02chatpubkey" {
		t.Fatalf("Profile.ChatPubkey = %q", got.Profile.ChatPubkey)
	}
	if got.Profile.Avatar == nil {
		t.Fatal("Profile.Avatar = nil, want avatar block")
	}
	if got.Profile.Avatar.PinId != "avatar-pin:i0" {
		t.Fatalf("Profile.Avatar.PinId = %q", got.Profile.Avatar.PinId)
	}
	if got.Profile.Avatar.ContentType != "image/png" {
		t.Fatalf("Profile.Avatar.ContentType = %q, want image/png", got.Profile.Avatar.ContentType)
	}
	if got.Profile.Pins.Name != "name-pin:i0" {
		t.Fatalf("Profile.Pins.Name = %q", got.Profile.Pins.Name)
	}
	if got.Profile.Pins.Bio != "bio-pin:i0" {
		t.Fatalf("Profile.Pins.Bio = %q", got.Profile.Pins.Bio)
	}
	if got.Profile.Pins.ChatPubkey != "chat-pin:i0" {
		t.Fatalf("Profile.Pins.ChatPubkey = %q", got.Profile.Pins.ChatPubkey)
	}
	assertJSONBlockV3Field(t, got.Profile.LLM, "llm-pin:i0", "provider", "openai")
	assertJSONBlockV3Field(t, got.Profile.LLM, "llm-pin:i0", "model", "gpt-4.1")
	assertJSONBlockV3Field(t, got.Profile.Persona, "persona-pin:i0", "style", "direct")
	assertJSONBlockV3Field(t, got.Profile.Persona, "persona-pin:i0", "language", "zh-CN")
	assertJSONBlockV3Field(t, got.Profile.Homepage, "homepage-pin:i0", "uri", "metaapp://homepage")
	assertJSONBlockV3Field(t, got.Profile.Homepage, "homepage-pin:i0", "renderer", "metaapp")
	if len(got.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want empty", got.Warnings)
	}
}

func TestBuildV3InvalidJSONBlocksReturnNullWithWarnings(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "IDQ Bot",
		LLM:          `{"provider":`,
		LLMId:        "llm-pin:i0",
		Persona:      `{"style":`,
		PersonaId:    "persona-pin:i0",
		Homepage:     `{"uri":`,
		HomepageId:   "homepage-pin:i0",
	}})

	got, err := agg.BuildV3("idqBot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}

	if got.Profile.LLM != nil {
		t.Fatalf("Profile.LLM = %+v, want nil", got.Profile.LLM)
	}
	if got.Profile.Persona != nil {
		t.Fatalf("Profile.Persona = %+v, want nil", got.Profile.Persona)
	}
	if got.Profile.Homepage != nil {
		t.Fatalf("Profile.Homepage = %+v, want nil", got.Profile.Homepage)
	}
	assertWarnings(t, got.Warnings, []string{
		"invalid JSON in /info/llm",
		"invalid JSON in /info/persona",
		"invalid JSON in /info/homepage",
	})
}

func TestBuildV3TopLevelShapeExcludesV2Fields(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		MetaId:       "metaBot",
		Name:         "IDQ Bot",
	}})

	got, err := agg.BuildV3("idqBot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	for _, key := range []string{"globalMetaId", "canonical", "persona", "homepage", "services", "actions", "proofs", "source", "resolvedAt"} {
		if _, ok := decoded[key]; ok {
			t.Fatalf("top-level key %q present in v3 payload: %s", key, string(encoded))
		}
	}
}

func TestBuildV3SectionsAreServicesBuzzesMetaapps(t *testing.T) {
	t.Run("loads sections in v3 order from homepage and published content listers", func(t *testing.T) {
		agg := &Aggregator{}
		if err := agg.Init(nil, nil); err != nil {
			t.Fatalf("Init returned error: %v", err)
		}
		agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
			GlobalMetaId: "idqCanonicalBot",
			MetaId:       "metaBot",
			Address:      "1BotAddress",
			ChainName:    "mvc",
			Name:         "Section Bot",
		}})

		serviceLister := &recordingHomepageServiceLister{result: &skillservice.HomepageListResult{
			List: []skillservice.ServiceListItem{{
				CurrentPinId:       "service-current:i0",
				SourceServicePinId: "service-source:i0",
				ServiceName:        "chain-reader",
				DisplayName:        "Chain Reader",
				Description:        "Reads chain state",
				ServiceIcon:        "metafile://service-icon",
				ProviderSkill:      "read-chain",
				OutputType:         "text",
				Price:              "0.10",
				Currency:           "SPACE",
				SettlementKind:     "address",
				PaymentAddress:     "1payAddress",
				UpdatedAt:          1710000002,
			}},
			HasMore: true,
		}}
		agg.SetHomepageServiceLister(serviceLister)

		contentLister := &protocolRecordingPublishedContentLister{
			results: map[string]*publishedcontent.ListResult{
				publishedcontent.PathSimpleBuzz: {
					Items: []publishedcontent.SectionItem{{
						SourcePinId:    "buzz-source:i0",
						CurrentPinId:   "buzz-current:i0",
						ProtocolPath:   publishedcontent.PathSimpleBuzz,
						PayloadText:    "latest buzz",
						PayloadExposed: true,
						CreatedAt:      1710000003,
					}},
					HasMore: true,
				},
				publishedcontent.PathMetaApp: {
					Items: []publishedcontent.SectionItem{{
						SourcePinId:  "metaapp-source:i0",
						CurrentPinId: "metaapp-current:i0",
						ProtocolPath: publishedcontent.PathMetaApp,
						PayloadJSON: map[string]any{
							"title": "Homepage MetaApp",
							"kind":  "tool",
						},
						PayloadExposed: true,
						UpdatedAt:      1710000004,
					}},
				},
			},
		}
		agg.SetPublishedContentLister(contentLister)

		opts := defaultV3Options()
		opts.IncludeInactiveServices = true
		got, err := agg.BuildV3("idqRequestedBot", opts)
		if err != nil {
			t.Fatalf("BuildV3 returned error: %v", err)
		}

		assertExactSectionIDsV3(t, got.Sections, []string{"services", "buzzes", "metaapps"})

		services := got.Sections[0]
		if services.ProtocolPath != skillservice.PathSkillService {
			t.Fatalf("services.ProtocolPath = %q, want %q", services.ProtocolPath, skillservice.PathSkillService)
		}
		if services.Page.Limit != homepageSectionLimit || services.Page.Count != 1 || !services.Page.HasMore {
			t.Fatalf("services.Page = %+v", services.Page)
		}
		if len(services.Items) != 1 {
			t.Fatalf("services.Items length = %d, want 1", len(services.Items))
		}
		if services.Items[0].PinId != "service-current:i0" {
			t.Fatalf("services.Items[0].PinId = %q, want current pin id", services.Items[0].PinId)
		}
		if services.Items[0].Timestamp != 1710000002 {
			t.Fatalf("services.Items[0].Timestamp = %d, want UpdatedAt", services.Items[0].Timestamp)
		}
		if services.Items[0].Data.Payload == nil {
			t.Fatal("services.Items[0].Data.Payload = nil, want allow-list payload")
		}
		if serviceLister.gotParams.ProviderGlobalMetaId != "idqCanonicalBot" {
			t.Fatalf("service lister ProviderGlobalMetaId = %q, want idqCanonicalBot", serviceLister.gotParams.ProviderGlobalMetaId)
		}
		if serviceLister.gotParams.Size != homepageSectionReadSize {
			t.Fatalf("service lister Size = %d, want %d", serviceLister.gotParams.Size, homepageSectionReadSize)
		}
		if !serviceLister.gotParams.IncludeInactive {
			t.Fatal("service lister IncludeInactive = false, want true")
		}
		if serviceLister.gotParams.ChainName != "" {
			t.Fatalf("service lister ChainName = %q, want empty", serviceLister.gotParams.ChainName)
		}

		buzzes := got.Sections[1]
		if buzzes.ProtocolPath != publishedcontent.PathSimpleBuzz {
			t.Fatalf("buzzes.ProtocolPath = %q, want %q", buzzes.ProtocolPath, publishedcontent.PathSimpleBuzz)
		}
		if len(buzzes.Items) != 1 {
			t.Fatalf("buzzes.Items length = %d, want 1", len(buzzes.Items))
		}
		if buzzes.Items[0].PinId != "buzz-current:i0" {
			t.Fatalf("buzzes.Items[0].PinId = %q, want current pin id", buzzes.Items[0].PinId)
		}
		if buzzes.Items[0].Timestamp != 1710000003 {
			t.Fatalf("buzzes.Items[0].Timestamp = %d, want CreatedAt", buzzes.Items[0].Timestamp)
		}
		if payload, ok := buzzes.Items[0].Data.Payload.(string); !ok || payload != "latest buzz" {
			t.Fatalf("buzzes.Items[0].Data.Payload = %#v, want latest buzz", buzzes.Items[0].Data.Payload)
		}

		metaapps := got.Sections[2]
		if metaapps.ProtocolPath != publishedcontent.PathMetaApp {
			t.Fatalf("metaapps.ProtocolPath = %q, want %q", metaapps.ProtocolPath, publishedcontent.PathMetaApp)
		}
		if len(metaapps.Items) != 1 {
			t.Fatalf("metaapps.Items length = %d, want 1", len(metaapps.Items))
		}
		if payload, ok := metaapps.Items[0].Data.Payload.(map[string]any); !ok || payload["title"] != "Homepage MetaApp" || payload["kind"] != "tool" {
			t.Fatalf("metaapps.Items[0].Data.Payload = %#v, want JSON payload", metaapps.Items[0].Data.Payload)
		}

		if len(contentLister.gotParams) != 6 {
			t.Fatalf("published content calls = %d, want 6 identity queries across buzzes/metaapps; params=%+v", len(contentLister.gotParams), contentLister.gotParams)
		}
		assertPublishedContentProtocolCalls(t, contentLister.gotParams, publishedcontent.PathSimpleBuzz, 3)
		assertPublishedContentProtocolCalls(t, contentLister.gotParams, publishedcontent.PathMetaApp, 3)
		for _, params := range contentLister.gotParams {
			if params.ChainName != "" {
				t.Fatalf("published content ChainName = %q, want empty for v3 sections", params.ChainName)
			}
		}
		if len(got.Warnings) != 0 {
			t.Fatalf("Warnings = %v, want empty", got.Warnings)
		}
	})

	t.Run("returns empty sections with warnings when a section source fails", func(t *testing.T) {
		agg := &Aggregator{}
		if err := agg.Init(nil, nil); err != nil {
			t.Fatalf("Init returned error: %v", err)
		}
		agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
			GlobalMetaId: "idqCanonicalBot",
			MetaId:       "metaBot",
			Address:      "1BotAddress",
			Name:         "Section Bot",
		}})
		agg.SetHomepageServiceLister(&recordingHomepageServiceLister{err: errors.New("services unavailable")})
		agg.SetPublishedContentLister(&protocolRecordingPublishedContentLister{
			errs: map[string]error{
				publishedcontent.PathSimpleBuzz: errors.New("buzzes unavailable"),
				publishedcontent.PathMetaApp:    errors.New("metaapps unavailable"),
			},
		})

		got, err := agg.BuildV3("idqRequestedBot", defaultV3Options())
		if err != nil {
			t.Fatalf("BuildV3 returned error: %v", err)
		}

		assertExactSectionIDsV3(t, got.Sections, []string{"services", "buzzes", "metaapps"})
		for _, section := range got.Sections {
			if len(section.Items) != 0 {
				t.Fatalf("section %q items = %+v, want empty after source error", section.ID, section.Items)
			}
		}
		assertWarnings(t, got.Warnings, []string{
			"services section source unavailable",
			"buzzes section source unavailable",
			"metaapps section source unavailable",
		})
	})
}

func TestBuildV3SectionItemsAreMinimal(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqCanonicalBot",
		MetaId:       "metaBot",
		Address:      "1BotAddress",
		ChainName:    "btc",
		Name:         "Minimal Bot",
	}})
	agg.SetHomepageServiceLister(&recordingHomepageServiceLister{result: &skillservice.HomepageListResult{
		List: []skillservice.ServiceListItem{{
			SourceServicePinId: "service-source:i0",
			ServiceName:        "deep-read",
			DisplayName:        "Deep Read",
			Description:        "Strict payload",
			ServiceIcon:        "metafile://service-icon",
			ProviderSkill:      "qa",
			OutputType:         "json",
			Price:              "1.00",
			Currency:           "BTC",
			SettlementKind:     "address",
			PaymentAddress:     "1btcPayment",
			ProviderName:       "Provider Name",
			ProviderAvatar:     "metafile://avatar",
			ProviderChatPubkey: "02pub",
			RatingAvg:          4.7,
			RatingCount:        9,
			Status:             1,
			Operation:          "modify",
			ChainName:          "btc",
			CreatedAt:          1710000010,
			UpdatedAt:          1710000011,
		}},
	}})
	agg.SetPublishedContentLister(&protocolRecordingPublishedContentLister{
		results: map[string]*publishedcontent.ListResult{
			publishedcontent.PathSimpleBuzz: {
				Items: []publishedcontent.SectionItem{{
					SourcePinId:    "buzz-source:i0",
					ProtocolPath:   publishedcontent.PathSimpleBuzz,
					PayloadText:    "payload text",
					PayloadExposed: true,
					CreatedAt:      1710000012,
				}},
			},
			publishedcontent.PathMetaApp: {
				Items: []publishedcontent.SectionItem{
					{
						SourcePinId:  "metaapp-json-source:i0",
						ProtocolPath: publishedcontent.PathMetaApp,
						PayloadJSON: map[string]any{
							"title": "MetaApp JSON",
							"kind":  "utility",
						},
						PayloadExposed: true,
						UpdatedAt:      1710000013,
					},
					{
						SourcePinId:    "metaapp-hidden-source:i0",
						ProtocolPath:   publishedcontent.PathMetaApp,
						ContentType:    "image/png",
						PayloadExposed: false,
						UpdatedAt:      1710000014,
					},
				},
			},
		},
	})

	got, err := agg.BuildV3("idqRequestedBot", defaultV3Options())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}
	assertExactSectionIDsV3(t, got.Sections, []string{"services", "buzzes", "metaapps"})

	services := got.Sections[0]
	if len(services.Items) != 1 {
		t.Fatalf("services.Items length = %d, want 1", len(services.Items))
	}
	if services.Items[0].PinId != "service-source:i0" {
		t.Fatalf("services.Items[0].PinId = %q, want source pin fallback", services.Items[0].PinId)
	}
	servicePayload, ok := services.Items[0].Data.Payload.(map[string]any)
	if !ok {
		t.Fatalf("services.Items[0].Data.Payload = %#v, want object", services.Items[0].Data.Payload)
	}
	assertMapHasOnlyKeys(t, servicePayload, []string{
		"serviceName",
		"displayName",
		"description",
		"providerSkill",
		"outputType",
		"price",
		"currency",
		"settlementKind",
		"paymentAddress",
	})
	if servicePayload["serviceName"] != "deep-read" || servicePayload["paymentAddress"] != "1btcPayment" {
		t.Fatalf("service payload = %#v, want allow-list values", servicePayload)
	}

	metaapps := got.Sections[2]
	if len(metaapps.Items) != 1 {
		t.Fatalf("metaapps.Items length = %d, want 1 exposed payload item", len(metaapps.Items))
	}
	metaappPayload, ok := metaapps.Items[0].Data.Payload.(map[string]any)
	if !ok {
		t.Fatalf("metaapps.Items[0].Data.Payload = %#v, want object", metaapps.Items[0].Data.Payload)
	}
	if _, nested := metaappPayload["payload"]; nested {
		t.Fatalf("metaapps.Items[0].Data.Payload = %#v, want direct payload instead of nested payload key", metaappPayload)
	}
	if metaappPayload["title"] != "MetaApp JSON" || metaappPayload["kind"] != "utility" {
		t.Fatalf("metaapp payload = %#v, want flattened published content payload", metaappPayload)
	}

	assertMinimalSectionItemV3JSON(t, services.Items[0])
	assertMinimalSectionItemV3JSON(t, metaapps.Items[0])
	assertMinimalSectionItemV3JSON(t, got.Sections[1].Items[0])
}

func TestBuildV3ServicesSectionFallsBackToProviderVisibleServices(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	cacheProvider := cache.New(store)

	userAgg := &userinfo.Aggregator{}
	if err := userAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("userinfo.Init returned error: %v", err)
	}
	skillAgg := &skillservice.Aggregator{}
	if err := skillAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("skillservice.Init returned error: %v", err)
	}
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("bothomepage.Init returned error: %v", err)
	}

	agg.SetProfileLookup(NewUserInfoLookupAdapter(userAgg))
	agg.SetServiceLister(skillAgg)
	agg.SetHomepageServiceLister(skillAgg)
	skillAgg.SetProfileLookup(skillservice.NewUserInfoLookupAdapter(userAgg))

	const (
		globalMetaID = "idq-legacy-provider"
		metaID       = "meta-legacy-provider"
		address      = "addr-legacy-provider"
		servicePinID = "legacy-service-current:i0"
	)

	for _, pin := range []*aggregator.PinInscription{
		{
			Id:           "init-legacy-provider:i0",
			Path:         "/",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			Timestamp:    1710000000,
		},
		{
			Id:           "name-legacy-provider:i0",
			Path:         "/info/name",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			ContentBody:  []byte("Legacy Provider"),
			Timestamp:    1710000001,
		},
	} {
		if _, err := userAgg.HandleBlockPin(pin); err != nil {
			t.Fatalf("userinfo.HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	if _, err := skillAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            servicePinID,
		Path:          skillservice.PathSkillService,
		Operation:     skillservice.OperationCreate,
		ContentBody:   []byte(`{"serviceName":"legacy-visible","displayName":"Legacy Visible","providerSkill":"legacy-skill","outputType":"text","price":"1","currency":"SPACE","settlementKind":"address","paymentAddress":"addr-legacy-provider"}`),
		ContentType:   "application/json",
		ChainName:     "mvc",
		GlobalMetaId:  globalMetaID,
		MetaId:        metaID,
		CreateMetaId:  metaID,
		Address:       address,
		CreateAddress: address,
		Timestamp:     1710000020,
		Number:        120,
	}); err != nil {
		t.Fatalf("skillservice.HandleBlockPin: %v", err)
	}

	for _, prefix := range [][]byte{
		[]byte("service_by_provider_global:" + globalMetaID + ":"),
		[]byte("service_by_provider_global_chain:" + globalMetaID + ":"),
		[]byte("service_by_provider_meta:" + metaID + ":"),
	} {
		if err := store.DeleteByPrefix(skillservice.NamespaceService, prefix); err != nil {
			t.Fatalf("delete legacy homepage index prefix %q: %v", string(prefix), err)
		}
	}

	v2Opts := DefaultOptions()
	v2Opts.Version = "v2"
	v2Opts.IncludeServices = true
	v2, err := agg.Build(globalMetaID, v2Opts)
	if err != nil {
		t.Fatalf("Build v2 returned error: %v", err)
	}
	if len(v2.Services) != 1 {
		t.Fatalf("v2 provider-visible services length = %d, want 1: %+v", len(v2.Services), v2.Services)
	}
	if v2.Services[0].CurrentPinId != servicePinID {
		t.Fatalf("v2 service CurrentPinId = %q, want %q", v2.Services[0].CurrentPinId, servicePinID)
	}

	v3, err := agg.BuildV3(globalMetaID, defaultV3Options())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}
	assertExactSectionIDsV3(t, v3.Sections, []string{"services", "buzzes", "metaapps"})
	services := v3.Sections[0]
	if len(services.Items) != 1 {
		t.Fatalf("v3 services section length = %d, want 1 when v2 services are visible: %+v", len(services.Items), services.Items)
	}
	if services.Items[0].PinId != servicePinID {
		t.Fatalf("v3 service pinId = %q, want current service pin %q", services.Items[0].PinId, servicePinID)
	}
	if services.Items[0].Data.Payload == nil {
		t.Fatal("v3 service payload = nil, want public payload")
	}
}

func TestBuildV3UsesMempoolProfileAndSectionPins(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	cacheProvider := cache.New(store)

	userAgg := &userinfo.Aggregator{}
	if err := userAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("userinfo.Init returned error: %v", err)
	}
	skillAgg := &skillservice.Aggregator{}
	if err := skillAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("skillservice.Init returned error: %v", err)
	}
	publishedAgg := &publishedcontent.Aggregator{}
	if err := publishedAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("publishedcontent.Init returned error: %v", err)
	}
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("bothomepage.Init returned error: %v", err)
	}
	agg.SetProfileLookup(NewUserInfoLookupAdapter(userAgg))
	agg.SetHomepageServiceLister(skillAgg)
	agg.SetPublishedContentLister(publishedAgg)

	const (
		globalMetaID = "idq-bot"
		metaID       = "meta-idq-bot"
		address      = "addr-idq-bot"
	)

	blockPins := []*aggregator.PinInscription{
		{
			Id:           "init-idq-bot:i0",
			Path:         "/",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			Timestamp:    1710000000,
		},
		{
			Id:           "name-idq-bot:i0",
			Path:         "/info/name",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			ContentBody:  []byte("Pending Bot"),
			Timestamp:    1710000001,
		},
	}
	for _, pin := range blockPins {
		if _, err := userAgg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Path, err)
		}
	}

	mempoolPins := []*aggregator.PinInscription{
		{
			Id:           "persona-pending:i0",
			Path:         "/info/persona",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			ContentBody:  []byte(`{"style":"direct"}`),
			ContentType:  "application/json",
			Timestamp:    1710000010,
		},
		{
			Id:           "llm-pending:i0",
			Path:         "/info/llm",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			ContentBody:  []byte(`{"provider":"openai","model":"gpt-4.1"}`),
			ContentType:  "application/json",
			Timestamp:    1710000011,
		},
		{
			Id:           "homepage-pending:i0",
			Path:         "/info/homepage",
			MetaId:       metaID,
			Address:      address,
			GlobalMetaId: globalMetaID,
			ChainName:    "mvc",
			ContentBody:  []byte(`{"uri":"metaapp://pending-homepage"}`),
			ContentType:  "application/json",
			Timestamp:    1710000012,
		},
	}
	for _, pin := range mempoolPins {
		if _, err := userAgg.HandleMempoolPin(pin); err != nil {
			t.Fatalf("userinfo.HandleMempoolPin(%s): %v", pin.Path, err)
		}
	}

	if _, err := skillAgg.HandleMempoolPin(&aggregator.PinInscription{
		Id:            "service-pending:i0",
		Path:          skillservice.PathSkillService,
		Operation:     skillservice.OperationCreate,
		ContentBody:   []byte(`{"serviceName":"fortune","displayName":"Fortune","providerSkill":"fortune-skill","outputType":"text","price":"1","currency":"SPACE","settlementKind":"address","paymentAddress":"addr-idq-bot"}`),
		ContentType:   "application/json",
		ChainName:     "mvc",
		GlobalMetaId:  globalMetaID,
		MetaId:        metaID,
		CreateMetaId:  metaID,
		Address:       address,
		CreateAddress: address,
		Timestamp:     1710000020,
		Number:        120,
	}); err != nil {
		t.Fatalf("skillservice.HandleMempoolPin: %v", err)
	}
	if _, err := publishedAgg.HandleMempoolPin(&aggregator.PinInscription{
		Id:           "buzz-pending:i0",
		Path:         publishedcontent.PathSimpleBuzz,
		Operation:    publishedcontent.OperationCreate,
		ContentBody:  []byte("pending buzz"),
		ContentType:  "text/plain",
		ChainName:    "mvc",
		GlobalMetaId: globalMetaID,
		MetaId:       metaID,
		Address:      address,
		Timestamp:    1710000030,
		Number:       130,
	}); err != nil {
		t.Fatalf("publishedcontent.HandleMempoolPin(simplebuzz): %v", err)
	}
	if _, err := publishedAgg.HandleMempoolPin(&aggregator.PinInscription{
		Id:           "metaapp-pending:i0",
		Path:         publishedcontent.PathMetaApp,
		Operation:    publishedcontent.OperationCreate,
		ContentBody:  []byte(`{"title":"Pending MetaAPP","kind":"tool"}`),
		ContentType:  "application/json",
		ChainName:    "mvc",
		GlobalMetaId: globalMetaID,
		MetaId:       metaID,
		Address:      address,
		Timestamp:    1710000040,
		Number:       140,
	}); err != nil {
		t.Fatalf("publishedcontent.HandleMempoolPin(metaapp): %v", err)
	}

	got, err := agg.BuildV3(globalMetaID, defaultV3Options())
	if err != nil {
		t.Fatalf("BuildV3 returned error: %v", err)
	}

	if got.Profile.Persona == nil || got.Profile.Persona.PinId != "persona-pending:i0" {
		t.Fatalf("Profile.Persona = %+v, want pending persona pin", got.Profile.Persona)
	}
	if got.Profile.LLM == nil || got.Profile.LLM.PinId != "llm-pending:i0" {
		t.Fatalf("Profile.LLM = %+v, want pending llm pin", got.Profile.LLM)
	}
	if got.Profile.Homepage == nil || got.Profile.Homepage.PinId != "homepage-pending:i0" {
		t.Fatalf("Profile.Homepage = %+v, want pending homepage pin", got.Profile.Homepage)
	}

	assertExactSectionIDsV3(t, got.Sections, []string{"services", "buzzes", "metaapps"})

	services := got.Sections[0]
	if len(services.Items) != 1 {
		t.Fatalf("services.Items length = %d, want 1", len(services.Items))
	}
	if services.Items[0].PinId != "service-pending:i0" {
		t.Fatalf("services.Items[0].PinId = %q, want pending service pin", services.Items[0].PinId)
	}
	if services.Items[0].Data.Payload == nil {
		t.Fatal("services.Items[0].Data.Payload = nil, want payload")
	}

	buzzes := got.Sections[1]
	if len(buzzes.Items) != 1 {
		t.Fatalf("buzzes.Items length = %d, want 1", len(buzzes.Items))
	}
	if buzzes.Items[0].PinId != "buzz-pending:i0" {
		t.Fatalf("buzzes.Items[0].PinId = %q, want pending buzz pin", buzzes.Items[0].PinId)
	}
	if payload, ok := buzzes.Items[0].Data.Payload.(string); !ok || payload != "pending buzz" {
		t.Fatalf("buzzes.Items[0].Data.Payload = %#v, want pending buzz", buzzes.Items[0].Data.Payload)
	}

	metaapps := got.Sections[2]
	if len(metaapps.Items) != 1 {
		t.Fatalf("metaapps.Items length = %d, want 1", len(metaapps.Items))
	}
	if metaapps.Items[0].PinId != "metaapp-pending:i0" {
		t.Fatalf("metaapps.Items[0].PinId = %q, want pending metaapp pin", metaapps.Items[0].PinId)
	}
	metaappPayload, ok := metaapps.Items[0].Data.Payload.(map[string]any)
	if !ok {
		t.Fatalf("metaapps.Items[0].Data.Payload = %#v, want object", metaapps.Items[0].Data.Payload)
	}
	if metaappPayload["title"] != "Pending MetaAPP" {
		t.Fatalf("metaapp payload = %#v, want pending payload", metaappPayload)
	}
}

func assertJSONBlockV3Field(t *testing.T, block *JSONBlockV3, pinID, key string, want any) {
	t.Helper()
	if block == nil {
		t.Fatalf("JSONBlockV3 for key %q = nil", key)
	}
	if block.PinId != pinID {
		t.Fatalf("JSONBlockV3.PinId = %q, want %q", block.PinId, pinID)
	}
	if got := block.Payload[key]; got != want {
		t.Fatalf("JSONBlockV3.Payload[%q] = %#v, want %#v", key, got, want)
	}
}

func assertWarnings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Warnings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Warnings = %v, want %v", got, want)
		}
	}
}

func assertExactSectionIDsV3(t *testing.T, sections []SectionV3, want []string) {
	t.Helper()
	if len(sections) != len(want) {
		t.Fatalf("Sections length = %d, want %d; sections=%+v", len(sections), len(want), sections)
	}
	for i, id := range want {
		if sections[i].ID != id {
			t.Fatalf("Sections[%d].ID = %q, want %q; sections=%+v", i, sections[i].ID, id, sections)
		}
	}
}

func assertPublishedContentProtocolCalls(t *testing.T, params []publishedcontent.ListParams, protocolPath string, want int) {
	t.Helper()
	got := 0
	for _, param := range params {
		if param.ProtocolPath == protocolPath {
			got++
		}
	}
	if got != want {
		t.Fatalf("protocol path %q calls = %d, want %d; params=%+v", protocolPath, got, want, params)
	}
}

func assertMapHasOnlyKeys(t *testing.T, got map[string]any, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("map keys = %v, want %v", mapKeys(got), want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("map missing key %q; keys=%v", key, mapKeys(got))
		}
	}
}

func assertMinimalSectionItemV3JSON(t *testing.T, item SectionItemV3) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal section item: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("json.Unmarshal section item %s: %v", raw, err)
	}

	assertMapHasOnlyKeys(t, encoded, []string{"data", "pinId", "protocolPath", "timestamp"})
	for _, forbidden := range []string{"sourcePinId", "currentPinId", "createdAt", "updatedAt", "chainName", "publisher", "proof", "service", "payloadJson", "payloadText", "payloadExposed"} {
		if _, ok := encoded[forbidden]; ok {
			t.Fatalf("top-level key %q present in v3 section item JSON: %s", forbidden, raw)
		}
	}
	data, ok := encoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("encoded data = %#v, want object; raw=%s", encoded["data"], raw)
	}
	assertMapHasOnlyKeys(t, data, []string{"payload"})
	if _, ok := data["payload"]; !ok {
		t.Fatalf("encoded data missing payload: %s", raw)
	}
}

func mapKeys(in map[string]any) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	return keys
}

func TestCustomHomepagePlanSchema(t *testing.T) {
	custom, ok := parseCustomHomepage(`{"uri":"metafile://homepage-pin","renderer":"auto","x":{"nested":true}}`)
	if !ok {
		t.Fatal("parseCustomHomepage returned ok=false, want true")
	}
	raw, err := custom.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("custom homepage did not marshal as object: %v", err)
	}
	if decoded["uri"] != "metafile://homepage-pin" || decoded["renderer"] != "auto" {
		t.Fatalf("custom homepage fields did not round trip: %#v", decoded)
	}
	if _, ok := decoded["protocolPath"]; ok {
		t.Fatalf("custom homepage should not add protocolPath: %#v", decoded)
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
