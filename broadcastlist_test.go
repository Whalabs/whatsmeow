package whatsmeow

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func TestParseBroadcastListsNode(t *testing.T) {
	lid := types.NewJID("17875972681890", types.HiddenUserServer)
	pn := types.NewJID("554899827309", types.DefaultUserServer)
	resp := &waBinary.Node{Tag: "iq", Content: []waBinary.Node{{
		Tag: "lists",
		Content: []waBinary.Node{
			{Tag: "list", Attrs: waBinary.Attrs{"id": types.NewJID("1792973140", types.BroadcastServer), "name": "Clientes"},
				Content: []waBinary.Node{{Tag: "recipient", Attrs: waBinary.Attrs{"jid": lid, "pn_jid": pn}}}},
			{Tag: "list", Attrs: waBinary.Attrs{"id": types.NewJID("123", types.GroupServer)}},
		},
	}}}
	lists, err := parseBroadcastListsNode(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 {
		t.Fatalf("got %d lists, want only the @broadcast one", len(lists))
	}
	if lists[0].Name != "Clientes" || len(lists[0].Participants) != 1 {
		t.Fatalf("unexpected list: %+v", lists[0])
	}
	if p := lists[0].Participants[0]; p.LID != lid || p.PN != pn {
		t.Errorf("participant = %+v, want both identities", p)
	}
}

func TestSameParticipantsIgnoresOrder(t *testing.T) {
	a := types.BroadcastListParticipant{LID: types.NewJID("1", types.HiddenUserServer)}
	b := types.BroadcastListParticipant{LID: types.NewJID("2", types.HiddenUserServer)}
	if !sameParticipants([]types.BroadcastListParticipant{a, b}, []types.BroadcastListParticipant{b, a}) {
		t.Error("same members in another order must be equal")
	}
	if sameParticipants([]types.BroadcastListParticipant{a}, []types.BroadcastListParticipant{b}) {
		t.Error("different members must differ")
	}
}
