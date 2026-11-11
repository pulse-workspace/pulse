// Package protocol defines the Pulse wire protocol catalog.
//
// The authoritative source for frame codes, event types, and message
// content types lives here. SDK code generators (in their respective repos)
// consume these tables to emit strongly-typed clients.
package protocol

// Frame types sent over the WebSocket subprotocol `pulse.protocol.v1.msgpack`.
// 16-bit codes; upper 4 bits reserved for direction (0 = both, 1 = C→S, 2 = S→C).
const (
	FrameHello       uint16 = 1 // C→S — first frame after upgrade
	FrameWelcome     uint16 = 2 // S→C — handshake response with session info
	FrameSubscribe   uint16 = 3 // C→S — subscribe to a stream (presence, conversation)
	FrameUnsubscribe uint16 = 4 // C→S

	FrameSendMessage uint16 = 10 // C→S — send a message to a conversation
	FrameMessageAck  uint16 = 11 // S→C — server ack of send (with serverMsgID, seq)
	FrameMessageRecv uint16 = 12 // S→C — push a new message to subscribed clients
	FrameMessageRead uint16 = 13 // S→C — somebody read this message
	FrameMarkRead    uint16 = 14 // C→S — mark messages as read

	FrameTypingStart uint16 = 20 // C→S
	FrameTypingStop  uint16 = 21 // C→S

	FrameReactionAdd uint16 = 30 // C→S
	FrameReactionDel uint16 = 31 // C→S
	FrameReactionUpd uint16 = 32 // S→C — reaction added/removed by anyone

	FramePresenceUpd uint16 = 40 // S→C
	FrameConvUpd     uint16 = 50 // S→C
	FrameGroupUpd    uint16 = 60 // S→C
	FrameGroupMember uint16 = 61 // S→C

	FramePing uint16 = 80
	FramePong uint16 = 81

	FrameError  uint16 = 100 // S→C
	FrameResync uint16 = 101 // S→C — full state resync after reconnect

	// Session handoff (Pulse-specific, no sticky session required)
	FrameHandoffReq   uint16 = 102 // C→S — new gateway requests session takeover
	FrameHandoffReady uint16 = 103 // S→C — old gateway snapshot ready
	FrameHandoffDone  uint16 = 104 // C→S → S→C — handoff complete
)

// Event types appended to the event log.
// Stable string identifiers; SDKs and server both reference these.
const (
	EventUserCreated = "UserCreated"
	EventUserUpdated = "UserUpdated"
	EventUserDeleted = "UserDeleted"

	EventGroupCreated      = "GroupCreated"
	EventGroupDismissed    = "GroupDismissed"
	EventMemberAdded       = "MemberAdded"
	EventMemberRemoved     = "MemberRemoved"
	EventMemberRoleChanged = "MemberRoleChanged"

	EventConvCreated = "ConvCreated"
	EventConvUpdated = "ConvUpdated"

	EventMessageSent     = "MessageSent"
	EventMessageRecalled = "MessageRecalled"
	EventMessageRead     = "MessageRead"

	EventReactionAdded   = "ReactionAdded"
	EventReactionRemoved = "ReactionRemoved"

	EventPresenceUpdated = "PresenceUpdated"
)

// Message content types — standard IM content type identifiers.
const (
	ContentText         int = 101
	ContentImage        int = 102
	ContentAudio        int = 103
	ContentVideo        int = 104
	ContentFile         int = 105
	ContentLocation     int = 106
	ContentCustom       int = 107
	ContentQuote        int = 108
	ContentCard         int = 109
	ContentNotification int = 110
	ContentMerge        int = 111
	ContentFace         int = 112
	ContentRevoke       int = 113
	// ContentAttachment is a generic attachment slot — the payload
	// `content` map carries the attachment_id + a presigned download
	// URL, sourced from /api/v1/media/{id}.
	ContentAttachment int = 120
)

// Conversation types.
const (
	ConvSingle int = 1
	ConvGroup  int = 2
)

// Role codes within a group.
const (
	RoleMember string = "member"
	RoleAdmin  string = "admin"
	RoleOwner  string = "owner"
)
