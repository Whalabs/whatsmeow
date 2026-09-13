// Copyright (c) 2026 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package types

import "time"

// BroadcastListParticipant is one recipient of a broadcast list. WhatsApp stores both identities,
// and either may be empty depending on how the contact was added.
type BroadcastListParticipant struct {
	LID JID
	PN  JID
}

// BroadcastListInfo is a broadcast list as stored in app state. Unlike a group, a broadcast list is
// local to the sender: recipients never learn it exists, and messages arrive as normal 1:1 chats.
type BroadcastListInfo struct {
	JID          JID
	Name         string
	Participants []BroadcastListParticipant
	LabelIDs     []string
	Timestamp    time.Time
}
