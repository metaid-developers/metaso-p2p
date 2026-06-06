package groupchat

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Dispatches pins to the appropriate handler based on pin.Path.
// Returns a NotifyEvent if push notification is needed.
func (a *Aggregator) dispatchPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if pin == nil {
		return nil, nil
	}

	path := pin.Path
	if path == "" {
		return nil, nil
	}

	// Normalize path: remove leading/trailing slashes, convert to lowercase
	path = strings.Trim(path, "/")
	pathLower := strings.ToLower(path)

	switch {
	// Community operations
	case strings.HasSuffix(pathLower, "simplecommunity"):
		return a.handleCommunityCreate(pin)

	// Group creation
	case strings.HasSuffix(pathLower, "simplegroupcreate"):
		return a.handleGroupCreate(pin)

	// Group channel create/update
	case strings.HasSuffix(pathLower, "simplegroupchannel"):
		return a.handleGroupChannel(pin)

	// Group admin/role changes
	case strings.HasSuffix(pathLower, "simplegroupadmin"):
		return a.handleGroupAdmin(pin)

	// Group block/mute
	case strings.HasSuffix(pathLower, "simplegroupblock"):
		return a.handleGroupBlock(pin)

	// Group chat message
	case strings.HasSuffix(pathLower, "simplegroupchat"):
		return a.handleGroupChat(pin)

	// Group join
	case strings.HasSuffix(pathLower, "simplegroupjoin"):
		return a.handleGroupJoin(pin)

	default:
		// Unknown path, ignore
		return nil, nil
	}
}

// Protocol message structures parsed from Pin ContentBody.

type SimpleCommunity struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Cover       string `json:"cover"`
	Icon        string `json:"icon"`
}

type SimpleGroupCreate struct {
	GroupId     string      `json:"groupId"`
	GroupName   string      `json:"groupName"`
	GroupIcon   string      `json:"groupIcon"`
	GroupNote   string      `json:"groupNote"`
	GroupType   string      `json:"groupType"`
	Status      string      `json:"status"`
	JoinType    interface{} `json:"joinType"`
	Type        interface{} `json:"type"`
	CommunityId string      `json:"communityId"`
}

type SimpleGroupChannel struct {
	GroupId     string      `json:"groupId"`
	ChannelId   string      `json:"channelId"`
	ChannelName string      `json:"channelName"`
	ChannelIcon string      `json:"channelIcon"`
	ChannelNote string      `json:"channelNote"`
	ChannelType interface{} `json:"channelType"`
}

type SimpleGroupAdmin struct {
	GroupId string   `json:"groupId"`
	Admins  []string `json:"admins"`
}

type SimpleGroupBlock struct {
	GroupId string   `json:"groupId"`
	Users   []string `json:"users"`
}

type SimpleGroupChat struct {
	GroupId     string   `json:"groupId"`
	ChannelId   string   `json:"channelId"`
	Content     string   `json:"content"`
	ContentType string   `json:"contentType"`
	Encryption  string   `json:"encryption"`
	ReplyPin    string   `json:"replyPin"`
	Mention     []string `json:"mention"`
}

type SimpleGroupJoin struct {
	GroupId  string      `json:"groupId"`
	Referrer string      `json:"referrer"`
	State    interface{} `json:"state"` // 1 = join, -1 = leave
	K        string      `json:"k"`
}

// Pin handler functions.

func (a *Aggregator) handleCommunityCreate(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sc SimpleCommunity
	if err := json.Unmarshal(pin.ContentBody, &sc); err != nil {
		log.Printf("[groupchat] failed to parse simplecommunity: %v", err)
		return nil, nil
	}

	metaId := pin.CreateMetaId
	if metaId == "" {
		metaId = pin.MetaId
	}

	community := &Community{
		CommunityId:   pin.Id,
		Name:          sc.Name,
		Description:   sc.Description,
		Cover:         sc.Cover,
		Icon:          sc.Icon,
		Creator:       pin.CreateAddress,
		CreatorMetaId: metaId,
		MemberCount:   1,
		CreatedAt:     pin.Timestamp,
		Chain:         pin.ChainName,
		BlockHeight:   pin.GenesisHeight,
	}

	if err := a.SaveCommunity(community); err != nil {
		return nil, err
	}

	log.Printf("[groupchat] community created: %s", community.CommunityId)
	return nil, nil
}

func (a *Aggregator) handleGroupCreate(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sgc SimpleGroupCreate
	if err := json.Unmarshal(pin.ContentBody, &sgc); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupcreate: %v", err)
		return nil, nil
	}

	groupId := sgc.GroupId
	if groupId == "" {
		groupId = pin.Id
	}

	metaId := pin.CreateMetaId
	if metaId == "" {
		metaId = pin.MetaId
	}

	group := &Group{
		GroupId:             groupId,
		TxId:                extractTxId(pin.Id),
		PinId:               pin.Id,
		GroupName:           sgc.GroupName,
		GroupNote:           sgc.GroupNote,
		GroupType:           firstProtocolString(sgc.GroupType, sgc.Type),
		Status:              sgc.Status,
		Avatar:              sgc.GroupIcon,
		Creator:             pin.CreateAddress,
		CreatorMetaId:       metaId,
		CreatorGlobalMetaId: pin.GlobalMetaId,
		MemberCount:         1,
		CommunityId:         sgc.CommunityId,
		JoinType:            firstProtocolString(sgc.JoinType, sgc.Type),
		CreatedAt:           pin.Timestamp,
		Chain:               pin.ChainName,
		BlockHeight:         pin.GenesisHeight,
	}

	if err := a.SaveGroup(group); err != nil {
		return nil, err
	}

	// Creator auto-joins as admin
	creatorMember := &GroupMember{
		MetaId:       metaId,
		GlobalMetaId: pin.GlobalMetaId,
		Address:      pin.CreateAddress,
		IsCreator:    true,
		IsAdmin:      true,
		Timestamp:    pin.Timestamp,
	}
	if err := a.SaveGroupMember(groupId, metaId, creatorMember); err != nil {
		return nil, err
	}

	// Store role
	creatorRole := &GroupMemberRole{
		MetaId:      metaId,
		GroupId:     groupId,
		IsCreator:   true,
		IsAdmin:     true,
		Timestamp:   pin.Timestamp,
		BlockHeight: pin.GenesisHeight,
	}
	if err := a.SetGroupRole(groupId, metaId, creatorRole); err != nil {
		return nil, err
	}

	if err := a.SaveGroupMetaIdJoinItem(groupId, &GroupMetaIdJoinItem{
		JoinPinId:      pin.Id,
		JoinType:       "create",
		JoinTimestamp:  pin.Timestamp,
		GroupState:     1,
		Address:        pin.CreateAddress,
		BlockHeight:    pin.GenesisHeight,
		Chain:          pin.ChainName,
		ByGlobalMetaId: pin.GlobalMetaId,
		ByMetaId:       metaId,
		ByAddress:      pin.CreateAddress,
	}); err != nil {
		return nil, err
	}

	log.Printf("[groupchat] group created: %s", groupId)
	return nil, nil
}

func (a *Aggregator) handleGroupChannel(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sgc SimpleGroupChannel
	if err := json.Unmarshal(pin.ContentBody, &sgc); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupchannel: %v", err)
		return nil, nil
	}

	groupId := sgc.GroupId
	if groupId == "" {
		return nil, nil
	}

	channelId := sgc.ChannelId
	if channelId == "" {
		channelId = pin.Id
	}

	metaId := pin.CreateMetaId
	if metaId == "" {
		metaId = pin.MetaId
	}

	channel := &GroupChannel{
		ChannelId:              channelId,
		GroupId:                groupId,
		TxId:                   extractTxId(pin.Id),
		PinId:                  pin.Id,
		ChannelName:            sgc.ChannelName,
		ChannelIcon:            sgc.ChannelIcon,
		ChannelNote:            sgc.ChannelNote,
		ChannelType:            protocolInt64Value(sgc.ChannelType, 0),
		CreateUserMetaId:       metaId,
		CreateUserGlobalMetaId: pin.GlobalMetaId,
		CreateUserAddress:      pin.CreateAddress,
		Timestamp:              pin.Timestamp,
		Chain:                  pin.ChainName,
		BlockHeight:            pin.GenesisHeight,
		Index:                  -1,
	}
	if channel.ChannelName == "" {
		channel.ChannelName = channelId
	}

	if err := a.SaveGroupChannel(channel); err != nil {
		return nil, err
	}

	log.Printf("[groupchat] group channel saved: channelId=%s groupId=%s", channelId, groupId)
	return nil, nil
}

func (a *Aggregator) handleGroupAdmin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sga SimpleGroupAdmin
	if err := json.Unmarshal(pin.ContentBody, &sga); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupadmin: %v", err)
		return nil, nil
	}

	groupId := sga.GroupId
	if groupId == "" {
		return nil, nil
	}

	var lastEvent *aggregator.NotifyEvent

	for _, adminMetaId := range sga.Admins {
		// Get or create member role
		role, _ := a.getGroupMemberRole(groupId, adminMetaId)
		if role == nil {
			role = &GroupMemberRole{
				MetaId:  adminMetaId,
				GroupId: groupId,
			}
		}
		role.IsAdmin = true
		role.Timestamp = pin.Timestamp
		role.BlockHeight = pin.GenesisHeight

		if err := a.SetGroupRole(groupId, adminMetaId, role); err != nil {
			log.Printf("[groupchat] failed to set admin role for %s in %s: %v", adminMetaId, groupId, err)
			continue
		}

		// Update member record
		member, _ := a.GetGroupMember(groupId, adminMetaId)
		if member != nil {
			member.IsAdmin = true
			member.Timestamp = pin.Timestamp
			if err := a.SaveGroupMember(groupId, adminMetaId, member); err != nil {
				log.Printf("[groupchat] failed to update member %s: %v", adminMetaId, err)
			}
		}

		// Build notify event
		lastEvent = &aggregator.NotifyEvent{
			Type:    "WS_SERVER_NOTIFY_GROUP_ROLE",
			MetaId:  adminMetaId,
			GroupId: groupId,
			Payload: map[string]interface{}{
				"metaId":    adminMetaId,
				"groupId":   groupId,
				"isAdmin":   true,
				"isCreator": role.IsCreator,
			},
		}
	}

	log.Printf("[groupchat] group admin set for %s: %v", groupId, sga.Admins)
	return lastEvent, nil
}

func (a *Aggregator) handleGroupBlock(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sgb SimpleGroupBlock
	if err := json.Unmarshal(pin.ContentBody, &sgb); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupblock: %v", err)
		return nil, nil
	}

	groupId := sgb.GroupId
	if groupId == "" {
		return nil, nil
	}

	var lastEvent *aggregator.NotifyEvent

	for _, blockedMetaId := range sgb.Users {
		role, _ := a.getGroupMemberRole(groupId, blockedMetaId)
		if role == nil {
			role = &GroupMemberRole{
				MetaId:  blockedMetaId,
				GroupId: groupId,
			}
		}
		role.IsBlocked = true
		role.Timestamp = pin.Timestamp
		role.BlockHeight = pin.GenesisHeight

		if err := a.SetGroupRole(groupId, blockedMetaId, role); err != nil {
			log.Printf("[groupchat] failed to set block role for %s in %s: %v", blockedMetaId, groupId, err)
			continue
		}

		lastEvent = &aggregator.NotifyEvent{
			Type:    "WS_SERVER_NOTIFY_GROUP_ROLE",
			MetaId:  blockedMetaId,
			GroupId: groupId,
			Payload: map[string]interface{}{
				"metaId":    blockedMetaId,
				"groupId":   groupId,
				"isBlocked": true,
			},
		}
	}

	log.Printf("[groupchat] group block set for %s: %v", groupId, sgb.Users)
	return lastEvent, nil
}

func (a *Aggregator) handleGroupChat(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sgc SimpleGroupChat
	if err := json.Unmarshal(pin.ContentBody, &sgc); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupchat: %v", err)
		return nil, nil
	}

	groupId := sgc.GroupId
	if groupId == "" {
		return nil, nil
	}

	metaId := pin.CreateMetaId
	if metaId == "" {
		metaId = pin.MetaId
	}

	// Ensure user is a member of the group
	isMember, _ := a.IsUserInGroup(groupId, metaId)
	if !isMember {
		// Auto-add as member if not already
		member := &GroupMember{
			MetaId:       metaId,
			GlobalMetaId: pin.GlobalMetaId,
			Address:      pin.CreateAddress,
			Timestamp:    pin.Timestamp,
		}
		a.SaveGroupMember(groupId, metaId, member)
	}

	// Resolve reply info if present
	var replyInfo *ChatMessage
	if sgc.ReplyPin != "" {
		replyInfo, _ = a.GetReplyInfo(sgc.ReplyPin)
	}

	chatType := "msg"
	if sgc.ContentType == "file" {
		chatType = "file"
	}

	msg := &ChatMessage{
		TxId:              extractTxId(pin.Id),
		PinId:             pin.Id,
		GroupId:           groupId,
		ChannelId:         sgc.ChannelId,
		MetaId:            metaId,
		GlobalMetaId:      pin.GlobalMetaId,
		Address:           pin.CreateAddress,
		Protocol:          pin.Path,
		Content:           sgc.Content,
		ContentType:       sgc.ContentType,
		Encryption:        sgc.Encryption,
		ChatType:          chatType,
		ReplyPin:          sgc.ReplyPin,
		ReplyMetaId:       pin.MetaId,
		ReplyGlobalMetaId: pin.GlobalMetaId,
		Mention:           sgc.Mention,
		Timestamp:         pin.Timestamp,
		Chain:             pin.ChainName,
		BlockHeight:       pin.GenesisHeight,
		Index:             -1,
	}

	if replyInfo != nil {
		msg.ReplyInfo = replyInfo
		msg.ReplyMetaId = replyInfo.MetaId
		msg.ReplyGlobalMetaId = replyInfo.GlobalMetaId
	}

	if err := a.SaveChatMessage(msg); err != nil {
		return nil, err
	}

	// Build notify event for socket push
	notifyEvent := &aggregator.NotifyEvent{
		Type:      "WS_SERVER_NOTIFY_GROUP_CHAT",
		GroupId:   groupId,
		TargetIds: a.GroupMemberTargetIds(groupId),
		Payload:   msg,
	}

	log.Printf("[groupchat] chat message saved: pinId=%s groupId=%s", msg.PinId, groupId)
	return notifyEvent, nil
}

func (a *Aggregator) handleGroupJoin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sgj SimpleGroupJoin
	if err := json.Unmarshal(pin.ContentBody, &sgj); err != nil {
		log.Printf("[groupchat] failed to parse simplegroupjoin: %v", err)
		return nil, nil
	}

	groupId := sgj.GroupId
	if groupId == "" {
		return nil, nil
	}

	metaId := pin.CreateMetaId
	if metaId == "" {
		metaId = pin.MetaId
	}

	// Determine join state: 1 = join, anything else = leave
	joinState := float64(1)
	if state, ok := sgj.State.(float64); ok {
		joinState = state
	}

	groupState := int64(1)
	joinType := "join"
	if joinState != 1 {
		groupState = -1
		joinType = "leave"
	}
	if err := a.SaveGroupMetaIdJoinItem(groupId, &GroupMetaIdJoinItem{
		JoinPinId:      pin.Id,
		JoinType:       joinType,
		JoinTimestamp:  pin.Timestamp,
		GroupState:     groupState,
		Address:        pin.CreateAddress,
		Referrer:       sgj.Referrer,
		K:              sgj.K,
		BlockHeight:    pin.GenesisHeight,
		Chain:          pin.ChainName,
		ByGlobalMetaId: pin.GlobalMetaId,
		ByMetaId:       metaId,
		ByAddress:      pin.CreateAddress,
	}); err != nil {
		return nil, err
	}

	if joinState == 1 {
		// Join
		member := &GroupMember{
			MetaId:       metaId,
			GlobalMetaId: pin.GlobalMetaId,
			Address:      pin.CreateAddress,
			IsRemoved:    false,
			Timestamp:    pin.Timestamp,
		}
		if err := a.SaveGroupMember(groupId, metaId, member); err != nil {
			return nil, err
		}

		// Update member count
		if group, _ := a.GetGroup(groupId); group != nil {
			group.MemberCount++
			a.SaveGroup(group)
		}

		// Build notify event
		return &aggregator.NotifyEvent{
			Type:    "WS_SERVER_NOTIFY_GROUP_ROLE",
			MetaId:  metaId,
			GroupId: groupId,
			Payload: map[string]interface{}{
				"metaId":  metaId,
				"groupId": groupId,
			},
		}, nil
	} else {
		// Leave
		member, _ := a.GetGroupMember(groupId, metaId)
		if member != nil {
			member.IsRemoved = true
			member.Timestamp = pin.Timestamp
			if err := a.SaveGroupMember(groupId, metaId, member); err != nil {
				return nil, err
			}
		}

		// Update member count
		if group, _ := a.GetGroup(groupId); group != nil && group.MemberCount > 0 {
			group.MemberCount--
			a.SaveGroup(group)
		}

		return &aggregator.NotifyEvent{
			Type:    "WS_SERVER_NOTIFY_GROUP_ROLE",
			MetaId:  metaId,
			GroupId: groupId,
			Payload: map[string]interface{}{
				"metaId":    metaId,
				"groupId":   groupId,
				"isRemoved": true,
			},
		}, nil
	}
}

// extractTxId extracts the txId from a pinId (txId:iN format).
func extractTxId(pinId string) string {
	idx := strings.LastIndex(pinId, "i")
	if idx > 0 {
		return pinId[:idx]
	}
	return pinId
}
