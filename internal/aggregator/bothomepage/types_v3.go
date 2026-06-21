package bothomepage

// DataV3 is the dedicated botHomepage.v3 response payload.
type DataV3 struct {
	SchemaVersion string      `json:"schemaVersion"`
	Identity      IdentityV3  `json:"identity"`
	Profile       ProfileV3   `json:"profile"`
	Presence      Presence    `json:"presence"`
	Sections      []SectionV3 `json:"sections"`
	Warnings      []string    `json:"warnings"`
}

type IdentityV3 struct {
	GlobalMetaId string `json:"globalMetaId"`
	LegacyMetaId string `json:"legacyMetaId,omitempty"`
	Display      string `json:"display"`
}

type ProfileV3 struct {
	Name       string        `json:"name"`
	Avatar     *AvatarV3     `json:"avatar"`
	Bio        string        `json:"bio"`
	ChatPubkey string        `json:"chatPubkey,omitempty"`
	LLM        *JSONBlockV3  `json:"llm"`
	Persona    *JSONBlockV3  `json:"persona"`
	Homepage   *JSONBlockV3  `json:"homepage"`
	Pins       ProfilePinsV3 `json:"pins"`
}

type AvatarV3 struct {
	PinId       string `json:"pinId"`
	ContentType string `json:"contentType"`
}

type JSONBlockV3 struct {
	PinId   string         `json:"pinId"`
	Payload map[string]any `json:"payload"`
}

type ProfilePinsV3 struct {
	Name       string `json:"name,omitempty"`
	Bio        string `json:"bio,omitempty"`
	ChatPubkey string `json:"chatPubkey,omitempty"`
}

type SectionV3 struct {
	ID           string          `json:"id"`
	ProtocolPath string          `json:"protocolPath"`
	Page         SectionPageV3   `json:"page"`
	Items        []SectionItemV3 `json:"items"`
}

type SectionPageV3 struct {
	Limit   int  `json:"limit"`
	Count   int  `json:"count"`
	HasMore bool `json:"hasMore"`
}

type SectionItemV3 struct {
	PinId        string            `json:"pinId"`
	ProtocolPath string            `json:"protocolPath"`
	Timestamp    int64             `json:"timestamp"`
	Data         SectionItemDataV3 `json:"data"`
}

type SectionItemDataV3 struct {
	Payload      any             `json:"payload,omitempty"`
	InteractWith *InteractWithV3 `json:"interactWith,omitempty"`
}

type InteractWithV3 struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
}
