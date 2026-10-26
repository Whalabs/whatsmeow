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
	"slices"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func (cli *Client) getBroadcastListParticipants(ctx context.Context, jid types.JID) ([]types.JID, error) {
	ownID := cli.getOwnID().ToNonAD()
	if ownID.IsEmpty() {
		return nil, ErrNotLoggedIn
	}
	if jid != types.StatusBroadcastJID {
		list, err := cli.getRegularBroadcastListRecipients(ctx, jid)
		if err != nil {
			return nil, err
		}
		// Our own devices are left out on purpose: holding the list's sender key, the phone would
		// decrypt the fan-out copy directly and never record the message as sent. Without it, it asks
		// for a retry and gets the message encrypted for it alone, which it does record.
		ownLID := cli.getOwnLID().ToNonAD()
		return slices.DeleteFunc(list, func(p types.JID) bool {
			return p.User == ownID.User || p.User == ownLID.User
		}), nil
	}
	list, err := cli.getStatusBroadcastRecipients(ctx)
	if err != nil {
		return nil, err
	}
	// Recipients are all LIDs, so we are too: the server refuses a list that mixes both identities.
	if ownLID := cli.getOwnLID().ToNonAD(); !ownLID.IsEmpty() {
		ownID = ownLID
	}
	if !slices.ContainsFunc(list, func(p types.JID) bool { return p.User == ownID.User }) {
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
		return cli.statusRecipientsAsLIDs(ctx, statusPrivacy.List), nil
	}

	// Blacklist or all contacts mode. Find all contacts from database, then filter them appropriately.
	contacts, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get contact list from db: %w", err)
	}

	var contactsArray []types.JID
	for jid, contact := range contacts {
		// TODO should there be a better way to separate contacts and found push names in the db?
		if len(contact.FullName) > 0 {
			contactsArray = append(contactsArray, jid)
		}
	}
	contactsArray = cli.statusRecipientsAsLIDs(ctx, contactsArray)
	if statusPrivacy.Type == types.StatusPrivacyTypeBlacklist {
		// Compared after both sides are LIDs: the privacy list and the contact store may name the same
		// person by different identities.
		blacklist := make(map[string]struct{})
		for _, jid := range cli.statusRecipientsAsLIDs(ctx, statusPrivacy.List) {
			blacklist[jid.User] = struct{}{}
		}
		contactsArray = slices.DeleteFunc(contactsArray, func(j types.JID) bool {
			_, ok := blacklist[j.User]
			return ok
		})
	}
	return contactsArray, nil
}

// statusRecipientsAsLIDs addresses every status recipient by LID. The contact store mixes phone and
// LID keys, and a mixed list is refused by the server, while a contact addressed by phone after the
// LID migration resolves to no devices and silently never gets the status. A phone with no known LID
// is dropped and counted, since it could not be delivered anyway.
func (cli *Client) statusRecipientsAsLIDs(ctx context.Context, jids []types.JID) []types.JID {
	var pns []types.JID
	for _, j := range jids {
		if j.Server == types.DefaultUserServer {
			pns = append(pns, j.ToNonAD())
		}
	}
	lidByPN, err := cli.Store.LIDs.GetManyLIDsForPNs(ctx, pns)
	if err != nil {
		cli.Log.Warnf("Failed to map status recipients to LIDs: %v", err)
	}
	seen := make(map[string]struct{}, len(jids))
	out := make([]types.JID, 0, len(jids))
	dropped := 0
	for _, j := range jids {
		lid := j.ToNonAD()
		if j.Server == types.DefaultUserServer {
			lid = lidByPN[lid]
		}
		if lid.Server != types.HiddenUserServer {
			dropped++
			continue
		}
		if _, dup := seen[lid.User]; dup {
			continue
		}
		seen[lid.User] = struct{}{}
		out = append(out, lid)
	}
	if dropped > 0 {
		cli.Log.Infof("Status recipients: %d by LID, %d dropped without a known LID", len(out), dropped)
	}
	return out
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
		// A list created on the phone after pairing is unknown until synced, so the server is asked once.
		if syncErr := cli.SyncBroadcastLists(ctx); syncErr != nil {
			return nil, fmt.Errorf("broadcast list not stored and server sync failed: %w", syncErr)
		}
		if info, err = cli.Store.BroadcastLists.GetBroadcastList(ctx, jid); err != nil {
			return nil, fmt.Errorf("failed to get broadcast list from store: %w", err)
		} else if info == nil {
			return nil, ErrBroadcastListNotFound
		}
	}
	recipients := make([]types.JID, 0, len(info.Participants))
	for _, p := range info.Participants {
		// Prefer the LID: the phone addresses lists by LID, and a PN-addressed send is refused
		// by the server with 420. The PN is the fallback for an entry that has no LID.
		if !p.LID.IsEmpty() {
			recipients = append(recipients, p.LID)
		} else if !p.PN.IsEmpty() {
			recipients = append(recipients, p.PN)
		}
	}
	if len(recipients) == 0 {
		return nil, ErrBroadcastListEmpty
	}
	return recipients, nil
}

// learnBroadcastList stores a list from a message the phone sent to it. App state may never carry
// the list (a full sync on a paired account brought none), so this echo, which has the list JID and
// its recipients, is how a companion learns it. A name already synced from app state is kept.
func (cli *Client) learnBroadcastList(ctx context.Context, info *types.MessageInfo) {
	if !info.IsFromMe || info.Chat.Server != types.BroadcastServer || info.Chat == types.StatusBroadcastJID ||
		len(info.BroadcastRecipients) == 0 || cli.Store.BroadcastLists == nil {
		return
	}
	list := types.BroadcastListInfo{
		JID:          info.Chat,
		Timestamp:    info.Timestamp,
		Participants: make([]types.BroadcastListParticipant, len(info.BroadcastRecipients)),
	}
	for i, r := range info.BroadcastRecipients {
		list.Participants[i] = types.BroadcastListParticipant{LID: r.LID, PN: r.PN}
	}
	existing, err := cli.Store.BroadcastLists.GetBroadcastList(ctx, info.Chat)
	if err != nil {
		cli.Log.Warnf("Failed to read broadcast list %s: %v", info.Chat, err)
		return
	}
	if existing != nil {
		// An older echo (offline backlog) must not overwrite a newer membership.
		if existing.Timestamp.After(info.Timestamp) || sameParticipants(existing.Participants, list.Participants) {
			return
		}
		list.Name = existing.Name
		list.LabelIDs = existing.LabelIDs
	}
	if err = cli.Store.BroadcastLists.PutBroadcastList(ctx, list); err != nil {
		cli.Log.Warnf("Failed to store broadcast list %s: %v", info.Chat, err)
	}
}

func sameParticipants(a, b []types.BroadcastListParticipant) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[types.BroadcastListParticipant]struct{}, len(a))
	for _, p := range a {
		seen[p] = struct{}{}
	}
	for _, p := range b {
		if _, ok := seen[p]; !ok {
			return false
		}
	}
	return true
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
	// The server is asked first: it knows every list of the account, including ones never used since
	// pairing. The store is the fallback when the query fails, so a hiccup does not empty the result.
	if err := cli.SyncBroadcastLists(ctx); err != nil {
		cli.Log.Warnf("Failed to sync broadcast lists from server, using stored ones: %v", err)
	}
	return cli.Store.BroadcastLists.GetAllBroadcastLists(ctx)
}

// FetchBroadcastListsFromServer queries the lists the server holds for this account. Each comes with
// its recipients (LID plus phone). The name is whatever the server has, which may be empty.
func (cli *Client) FetchBroadcastListsFromServer(ctx context.Context) ([]types.BroadcastListInfo, error) {
	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: "w:b",
		Type:      iqGet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{{Tag: "lists"}},
	})
	if err != nil {
		return nil, err
	}
	return parseBroadcastListsNode(resp)
}

func parseBroadcastListsNode(resp *waBinary.Node) ([]types.BroadcastListInfo, error) {
	listsNode, ok := resp.GetOptionalChildByTag("lists")
	if !ok {
		return nil, &ElementMissingError{Tag: "lists", In: "response to broadcast lists query"}
	}
	var out []types.BroadcastListInfo
	for _, child := range listsNode.GetChildren() {
		if child.Tag != "list" {
			continue
		}
		ag := child.AttrGetter()
		info := types.BroadcastListInfo{
			JID:  ag.JID("id"),
			Name: ag.OptionalString("name"),
		}
		if !ag.OK() || info.JID.Server != types.BroadcastServer {
			continue
		}
		for _, r := range child.GetChildren() {
			if r.Tag != "recipient" {
				continue
			}
			rag := r.AttrGetter()
			participant := types.BroadcastListParticipant{PN: rag.OptionalJIDOrEmpty("pn_jid")}
			if jid := rag.OptionalJIDOrEmpty("jid"); jid.Server == types.HiddenUserServer {
				participant.LID = jid
			} else if participant.PN.IsEmpty() {
				participant.PN = jid
			}
			if !participant.LID.IsEmpty() || !participant.PN.IsEmpty() {
				info.Participants = append(info.Participants, participant)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// SyncBroadcastLists stores what the server reports. A name already known (from Business app state)
// survives an empty server name, and a list the server no longer reports is removed unless it carries
// labels, which only Business app state sets and which the server query may not cover.
func (cli *Client) SyncBroadcastLists(ctx context.Context) error {
	lists, err := cli.FetchBroadcastListsFromServer(ctx)
	if err != nil {
		return err
	}
	stored, err := cli.Store.BroadcastLists.GetAllBroadcastLists(ctx)
	if err != nil {
		return fmt.Errorf("failed to read stored broadcast lists: %w", err)
	}
	storedByJID := make(map[types.JID]types.BroadcastListInfo, len(stored))
	for _, l := range stored {
		storedByJID[l.JID] = l
	}
	seen := make(map[types.JID]struct{}, len(lists))
	now := time.Now()
	for _, l := range lists {
		seen[l.JID] = struct{}{}
		if prev, ok := storedByJID[l.JID]; ok {
			if l.Name == "" {
				l.Name = prev.Name
			}
			l.LabelIDs = prev.LabelIDs
		}
		l.Timestamp = now
		if err = cli.Store.BroadcastLists.PutBroadcastList(ctx, l); err != nil {
			return fmt.Errorf("failed to store broadcast list %s: %w", l.JID, err)
		}
	}
	for _, l := range stored {
		if _, ok := seen[l.JID]; !ok && len(l.LabelIDs) == 0 {
			if err = cli.Store.BroadcastLists.DeleteBroadcastList(ctx, l.JID); err != nil {
				return fmt.Errorf("failed to delete stale broadcast list %s: %w", l.JID, err)
			}
		}
	}
	return nil
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
