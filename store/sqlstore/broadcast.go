// Copyright (c) 2026 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"go.mau.fi/whatsmeow/types"
)

const (
	putBroadcastListQuery = `
		INSERT INTO whatsmeow_broadcast_lists (our_jid, list_jid, name, participants, label_ids, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (our_jid, list_jid) DO UPDATE
			SET name=excluded.name, participants=excluded.participants,
			    label_ids=excluded.label_ids, updated_at=excluded.updated_at
	`
	deleteBroadcastListQuery = `
		DELETE FROM whatsmeow_broadcast_lists WHERE our_jid=$1 AND list_jid=$2
	`
	getBroadcastListQuery = `
		SELECT list_jid, name, participants, label_ids, updated_at
		FROM whatsmeow_broadcast_lists WHERE our_jid=$1 AND list_jid=$2
	`
	getAllBroadcastListsQuery = `
		SELECT list_jid, name, participants, label_ids, updated_at
		FROM whatsmeow_broadcast_lists WHERE our_jid=$1 ORDER BY name
	`
)

// storedParticipant is the on-disk shape. JIDs are stored as strings so a malformed entry degrades
// to an empty JID instead of failing the whole list.
type storedParticipant struct {
	LID string `json:"lid,omitempty"`
	PN  string `json:"pn,omitempty"`
}

func (s *SQLStore) PutBroadcastList(ctx context.Context, list types.BroadcastListInfo) error {
	participants := make([]storedParticipant, len(list.Participants))
	for i, p := range list.Participants {
		participants[i] = storedParticipant{LID: p.LID.String(), PN: p.PN.String()}
	}
	participantsJSON, err := json.Marshal(participants)
	if err != nil {
		return err
	}
	labelIDs := list.LabelIDs
	if labelIDs == nil {
		labelIDs = []string{}
	}
	labelsJSON, err := json.Marshal(labelIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, putBroadcastListQuery, s.JID, list.JID, list.Name,
		string(participantsJSON), string(labelsJSON), list.Timestamp.Unix())
	return err
}

func (s *SQLStore) DeleteBroadcastList(ctx context.Context, listJID types.JID) error {
	_, err := s.db.Exec(ctx, deleteBroadcastListQuery, s.JID, listJID)
	return err
}

func (s *SQLStore) GetBroadcastList(ctx context.Context, listJID types.JID) (*types.BroadcastListInfo, error) {
	var jidStr, name, participantsJSON, labelsJSON string
	var updatedAt int64
	err := s.db.QueryRow(ctx, getBroadcastListQuery, s.JID, listJID).
		Scan(&jidStr, &name, &participantsJSON, &labelsJSON, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	list, err := scanBroadcastList(jidStr, name, participantsJSON, labelsJSON, updatedAt)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

func (s *SQLStore) GetAllBroadcastLists(ctx context.Context) ([]types.BroadcastListInfo, error) {
	rows, err := s.db.Query(ctx, getAllBroadcastListsQuery, s.JID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lists []types.BroadcastListInfo
	for rows.Next() {
		var jidStr, name, participantsJSON, labelsJSON string
		var updatedAt int64
		if err := rows.Scan(&jidStr, &name, &participantsJSON, &labelsJSON, &updatedAt); err != nil {
			return nil, err
		}
		list, err := scanBroadcastList(jidStr, name, participantsJSON, labelsJSON, updatedAt)
		if err != nil {
			return nil, err
		}
		lists = append(lists, list)
	}
	return lists, rows.Err()
}

func scanBroadcastList(jidStr, name, participantsJSON, labelsJSON string, updatedAt int64) (types.BroadcastListInfo, error) {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return types.BroadcastListInfo{}, err
	}
	var stored []storedParticipant
	if err := json.Unmarshal([]byte(participantsJSON), &stored); err != nil {
		return types.BroadcastListInfo{}, err
	}
	participants := make([]types.BroadcastListParticipant, 0, len(stored))
	for _, sp := range stored {
		var p types.BroadcastListParticipant
		if sp.LID != "" {
			p.LID, _ = types.ParseJID(sp.LID)
		}
		if sp.PN != "" {
			p.PN, _ = types.ParseJID(sp.PN)
		}
		participants = append(participants, p)
	}
	var labelIDs []string
	if err := json.Unmarshal([]byte(labelsJSON), &labelIDs); err != nil {
		return types.BroadcastListInfo{}, err
	}
	return types.BroadcastListInfo{
		JID:          jid,
		Name:         name,
		Participants: participants,
		LabelIDs:     labelIDs,
		Timestamp:    time.Unix(updatedAt, 0),
	}, nil
}
