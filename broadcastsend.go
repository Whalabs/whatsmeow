// Copyright (c) 2026 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// addListRecipientPNs puts each recipient's phone on the node of its primary device, as the phone does.
// Without it the account's other companions only see the recipients by LID.
func (cli *Client) addListRecipientPNs(ctx context.Context, list types.JID, node *waBinary.Node) {
	info, err := cli.Store.BroadcastLists.GetBroadcastList(ctx, list)
	if err != nil || info == nil {
		return
	}
	pnByLID := make(map[string]types.JID, len(info.Participants))
	for _, p := range info.Participants {
		if !p.LID.IsEmpty() && !p.PN.IsEmpty() {
			pnByLID[p.LID.User] = p.PN.ToNonAD()
		}
	}
	participantsNode, ok := node.GetOptionalChildByTag("participants")
	if !ok {
		return
	}
	for _, to := range participantsNode.GetChildren() {
		jid, ok := to.Attrs["jid"].(types.JID)
		if !ok || jid.Server != types.HiddenUserServer || jid.Device != 0 {
			continue
		}
		if pn, found := pnByLID[jid.User]; found {
			to.Attrs["peer_recipient_pn"] = pn
		}
	}
}
