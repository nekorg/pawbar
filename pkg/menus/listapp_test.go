// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"testing"
	"time"

	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// hoverFixture is a panel showing: plain, submenu, plain, submenu.
func hoverFixture() (*listState, *[]wire.Msg) {
	st := newListState()
	st.items = []wire.Item{
		{ID: 1, Label: "plain"},
		{ID: 2, Label: "sub", HasSubmenu: true},
		{ID: 3, Label: "other"},
		{ID: 4, Label: "sub2", HasSubmenu: true},
	}
	sent := &[]wire.Msg{}
	st.send = func(m wire.Msg) { *sent = append(*sent, m) }
	st.req = func(id int32, row int) wire.Msg {
		return wire.Msg{Type: wire.MsgSubmenuReq, ItemID: id, Row: row}
	}
	return st, sent
}

func cancels(msgs []wire.Msg) int {
	n := 0
	for _, m := range msgs {
		if m.Type == wire.MsgSubmenuCancel {
			n++
		}
	}
	return n
}

// openRow2 walks the state to "the submenu of row 2 is up".
func openRow2(st *listState) {
	st.pointerTo(1)
	st.openFired()
}

func TestSubmenuSurvivesASlipOntoASibling(t *testing.T) {
	st, sent := hoverFixture()
	openRow2(st)
	if st.openID != 2 || st.openRow != 1 {
		t.Fatalf("submenu not open: id=%d row=%d", st.openID, st.openRow)
	}
	*sent = nil

	// Clipping a sibling on the way to the submenu must not close it.
	st.pointerTo(2)
	if n := cancels(*sent); n != 0 {
		t.Errorf("straying onto a sibling sent %d cancels, want 0", n)
	}
	if st.closeTimer == nil {
		t.Error("straying onto a sibling did not arm the close")
	}
	if st.openID != 2 {
		t.Errorf("openID = %d, want 2", st.openID)
	}

	// Crossing into the submenu: this panel only sees the pointer leave.
	st.pointerLeft()
	if st.closeTimer != nil {
		t.Error("leaving the panel left the close armed; it would shut the submenu the pointer is in")
	}
	if st.openID != 2 {
		t.Errorf("openID = %d after leaving, want 2", st.openID)
	}

	// And a slip back out of the item's bounds and onto it again.
	st.pointerTo(2)
	st.pointerTo(1)
	if n := cancels(*sent); n != 0 {
		t.Errorf("slipping out and back sent %d cancels, want 0", n)
	}
	if st.closeTimer != nil {
		t.Error("returning to the parent row left the close armed")
	}
	if st.openID != 2 {
		t.Errorf("openID = %d, want 2", st.openID)
	}
}

func TestSubmenuClosesOnceThePointerSettlesElsewhere(t *testing.T) {
	st, sent := hoverFixture()
	openRow2(st)
	*sent = nil

	st.pointerTo(2)
	select {
	case <-timerC(st.closeTimer):
	case <-time.After(submenuCloseGrace + time.Second):
		t.Fatal("close timer never fired")
	}
	st.closeFired()

	if n := cancels(*sent); n != 1 {
		t.Errorf("sent %d cancels, want 1", n)
	}
	if st.openID != 0 || st.openRow != -1 {
		t.Errorf("submenu still recorded open: id=%d row=%d", st.openID, st.openRow)
	}
}

func TestCrossingRowsGetsOneGraceNotOnePerRow(t *testing.T) {
	st, _ := hoverFixture()
	openRow2(st)

	st.pointerTo(2)
	first := st.closeTimer
	st.pointerTo(0)
	if st.closeTimer != first {
		t.Error("a second stray row restarted the grace; a stale submenu must have a bounded life")
	}
}

func TestHoveringAnotherSubmenuItemSupersedes(t *testing.T) {
	st, sent := hoverFixture()
	openRow2(st)
	*sent = nil

	st.pointerTo(3)
	if st.pendingID != 4 {
		t.Fatalf("pendingID = %d, want 4", st.pendingID)
	}
	st.openFired()

	if st.openID != 4 || st.openRow != 3 {
		t.Errorf("open submenu = id %d row %d, want id 4 row 3", st.openID, st.openRow)
	}
	if st.closeTimer != nil {
		t.Error("opening a new submenu left the old one's close armed")
	}
	// OpenSub closes whatever hangs below before spawning, so no cancel of
	// our own; one would race the request.
	if n := cancels(*sent); n != 0 {
		t.Errorf("sent %d cancels, want 0", n)
	}
	if len(*sent) == 0 || (*sent)[len(*sent)-1].Type != wire.MsgSubmenuReq {
		t.Errorf("last message = %+v, want a submenu request", *sent)
	}
}

func TestWobbleDoesNotRestartTheOpenCountdown(t *testing.T) {
	st, _ := hoverFixture()
	st.pointerTo(1)
	first := st.openTimer
	if first == nil {
		t.Fatal("hovering a submenu item did not arm the open")
	}
	st.armOpen(2, 1)
	if st.openTimer != first {
		t.Error("re-hovering the same row restarted the open countdown")
	}
}

func TestRebindFollowsTheRowThroughAnUpdate(t *testing.T) {
	st, _ := hoverFixture()
	openRow2(st)

	// A refresh reissues every id on this level; the rows are what last.
	st.items = []wire.Item{
		{ID: 11, Label: "plain"},
		{ID: 12, Label: "sub", HasSubmenu: true},
		{ID: 13, Label: "other"},
	}
	st.rebind()
	if st.openID != 12 || st.openRow != 1 {
		t.Errorf("open submenu = id %d row %d, want id 12 row 1", st.openID, st.openRow)
	}

	// The row the submenu hung off is gone.
	st.items = []wire.Item{{ID: 21, Label: "only"}}
	st.rebind()
	if st.openID != 0 || st.openRow != -1 {
		t.Errorf("open submenu = id %d row %d, want none", st.openID, st.openRow)
	}
}

func TestClickElsewhereClosesTheSubmenuAtOnce(t *testing.T) {
	st, sent := hoverFixture()
	openRow2(st)
	*sent = nil

	st.pointerTo(2)
	st.closeSub()
	if n := cancels(*sent); n != 1 {
		t.Errorf("sent %d cancels, want 1", n)
	}
	if st.closeTimer != nil {
		t.Error("an immediate close left the grace armed")
	}
}
