// Copyright (c) 2022 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func (cli *Client) getBroadcastListParticipants(ctx context.Context, jid types.JID) ([]types.JID, error) {
	var list []types.JID
	var err error
	if jid == types.StatusBroadcastJID {
		list, err = cli.getStatusBroadcastRecipients(ctx)
	} else {
		list, err = cli.getRegularBroadcastListRecipients(ctx, jid)
	}
	if err != nil {
		return nil, err
	}
	ownID := cli.getOwnID().ToNonAD()
	if ownID.IsEmpty() {
		return nil, ErrNotLoggedIn
	}

	selfIndex := -1
	for i, participant := range list {
		if participant.User == ownID.User {
			selfIndex = i
			break
		}
	}
	if selfIndex < 0 {
		list = append(list, ownID)
	}
	return list, nil
}

func (cli *Client) getStatusBroadcastRecipients(ctx context.Context) ([]types.JID, error) {
	statusPrivacyOptions, err := cli.GetStatusPrivacy(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get status privacy: %w", err)
	}
	statusPrivacy := statusPrivacyOptions[0]
	if statusPrivacy.Type == types.StatusPrivacyTypeWhitelist {
		// Whitelist mode, just return the list
		return statusPrivacy.List, nil
	}

	// Blacklist or all contacts mode. Find all contacts from database, then filter them appropriately.
	contacts, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get contact list from db: %w", err)
	}

	blacklist := make(map[types.JID]struct{})
	if statusPrivacy.Type == types.StatusPrivacyTypeBlacklist {
		for _, jid := range statusPrivacy.List {
			blacklist[jid] = struct{}{}
		}
	}

	var contactsArray []types.JID
	for jid, contact := range contacts {
		_, isBlacklisted := blacklist[jid]
		if isBlacklisted {
			continue
		}
		// TODO should there be a better way to separate contacts and found push names in the db?
		if len(contact.FullName) > 0 {
			contactsArray = append(contactsArray, jid)
		}
	}
	return contactsArray, nil
}

var DefaultStatusPrivacy = []types.StatusPrivacy{{
	Type:      types.StatusPrivacyTypeContacts,
	IsDefault: true,
}}

// GetStatusPrivacy gets the user's status privacy settings (who to send status broadcasts to).
//
// There can be multiple different stored settings, the first one is always the default.
func (cli *Client) GetStatusPrivacy(ctx context.Context) ([]types.StatusPrivacy, error) {
	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: "status",
		Type:      iqGet,
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag: "privacy",
		}},
	})
	if err != nil {
		if errors.Is(err, ErrIQNotFound) {
			return DefaultStatusPrivacy, nil
		}
		return nil, err
	}
	privacyLists := resp.GetChildByTag("privacy")
	var outputs []types.StatusPrivacy
	for _, list := range privacyLists.GetChildren() {
		if list.Tag != "list" {
			continue
		}

		ag := list.AttrGetter()
		var out types.StatusPrivacy
		out.IsDefault = ag.OptionalBool("default")
		out.Type = types.StatusPrivacyType(ag.String("type"))
		children := list.GetChildren()
		if len(children) > 0 {
			out.List = make([]types.JID, 0, len(children))
			for _, child := range children {
				jid, ok := child.Attrs["jid"].(types.JID)
				if child.Tag == "user" && ok {
					out.List = append(out.List, jid)
				}
			}
		}
		outputs = append(outputs, out)
		if out.IsDefault {
			// Move default to always be first in the list
			outputs[len(outputs)-1] = outputs[0]
			outputs[0] = out
		}
		if len(ag.Errors) > 0 {
			return nil, ag.Error()
		}
	}
	if len(outputs) == 0 {
		return DefaultStatusPrivacy, nil
	}
	return outputs, nil
}

// getRegularBroadcastListRecipients resolves the members of a non-status broadcast list from the
// app state synced copy. There is no server-side query for this: a broadcast list is local to the
// sender, so if app state never synced the list, it cannot be resolved.
func (cli *Client) getRegularBroadcastListRecipients(ctx context.Context, jid types.JID) ([]types.JID, error) {
	if cli.Store.BroadcastLists == nil {
		return nil, ErrBroadcastListUnsupported
	}
	info, err := cli.Store.BroadcastLists.GetBroadcastList(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("failed to get broadcast list from store: %w", err)
	} else if info == nil {
		return nil, ErrBroadcastListNotFound
	}
	recipients := make([]types.JID, 0, len(info.Participants))
	for _, p := range info.Participants {
		// Prefer the phone number: it is what the recipient's session is keyed by for contacts that
		// predate LID addressing. The LID is the fallback for username-only contacts.
		if !p.PN.IsEmpty() {
			recipients = append(recipients, p.PN)
		} else if !p.LID.IsEmpty() {
			recipients = append(recipients, p.LID)
		}
	}
	if len(recipients) == 0 {
		return nil, ErrBroadcastListEmpty
	}
	return recipients, nil
}

// GetBroadcastLists returns the broadcast lists synced from app state.
//
// Broadcast lists are local to the sender, so this reads the local copy rather than querying the
// server. An account that has never synced the "regular" app state patch returns an empty list.
func (cli *Client) GetBroadcastLists(ctx context.Context) ([]types.BroadcastListInfo, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	} else if cli.Store.BroadcastLists == nil {
		return nil, ErrBroadcastListUnsupported
	}
	return cli.Store.BroadcastLists.GetAllBroadcastLists(ctx)
}

// GetBroadcastListInfo returns one broadcast list synced from app state, or ErrBroadcastListNotFound.
func (cli *Client) GetBroadcastListInfo(ctx context.Context, jid types.JID) (*types.BroadcastListInfo, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	} else if cli.Store.BroadcastLists == nil {
		return nil, ErrBroadcastListUnsupported
	}
	info, err := cli.Store.BroadcastLists.GetBroadcastList(ctx, jid)
	if err != nil {
		return nil, err
	} else if info == nil {
		return nil, ErrBroadcastListNotFound
	}
	return info, nil
}
