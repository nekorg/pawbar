// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package dbusmenukitty

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/dbusmenukitty/menu"
	"github.com/nekorg/pawbar/pkg/dbusmenukitty/tui"
	"github.com/codelif/xdgicons"
	"github.com/fxamacker/cbor/v2"
	"github.com/godbus/dbus/v5"
)

var iconLookup = xdgicons.NewIconLookupWithConfig(xdgicons.LookupConfig{FallbackTheme: "Adwaita"})

type Layout struct {
	Id         int32
	Properties map[string]dbus.Variant
	Children   []Layout
}

type DBusMenuClient struct {
	conn    *dbus.Conn
	obj     dbus.BusObject
	busname string
	path    string
}

func NewDBusMenuClient(busname, path string) (*DBusMenuClient, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("error connecting to session bus: %w", err)
	}

	obj := conn.Object(busname, dbus.ObjectPath(path))

	return &DBusMenuClient{
		conn:    conn,
		obj:     obj,
		busname: busname,
		path:    path,
	}, nil
}

func (c *DBusMenuClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *DBusMenuClient) GetLayout() (Layout, error) {
	return c.GetLayoutForParent(0)
}

func (c *DBusMenuClient) GetLayoutForParent(parentId int32) (Layout, error) {
	call := c.obj.Call("com.canonical.dbusmenu.GetLayout", 0, parentId, -1, []string{})
	if call.Err != nil {
		return Layout{}, fmt.Errorf("error calling GetLayout: %w", call.Err)
	}

	var revision uint32
	var layout Layout
	err := call.Store(&revision, &layout)
	if err != nil {
		return Layout{}, fmt.Errorf("error storing layout: %w", err)
	}

	return layout, nil
}

func (c *DBusMenuClient) SendEvent(id int32, eventType string, data interface{}) error {
	timestamp := uint32(time.Now().Unix())
	call := c.obj.Call("com.canonical.dbusmenu.Event", 0, id, eventType, dbus.MakeVariant(data), timestamp)
	return call.Err
}

func (c *DBusMenuClient) AboutToShow(id int32) (bool, error) {
	call := c.obj.Call("com.canonical.dbusmenu.AboutToShow", 0, id)
	if call.Err != nil {
		return false, call.Err
	}

	var needUpdate bool
	err := call.Store(&needUpdate)
	return needUpdate, err
}

func LaunchMenu(x, y int) {
	busname := "org.freedesktop.network-manager-applet"
	path := "/org/ayatana/NotificationItem/nm_applet/Menu"

	// busname = "org.blueman.Tray"
	// path = "/org/blueman/sni/menu"

	client, err := NewDBusMenuClient(busname, path)
	if err != nil {
		log.Fatalf("error creating dbus client: %v", err)
	}
	defer client.Close()

	// Get status
	var status string
	client.obj.StoreProperty("com.canonical.dbusmenu.Status", &status)
	utils.Logger.Printf("Status: %s\n", status)

	// Get initial layout
	layout, err := client.GetLayout()
	if err != nil {
		log.Fatalf("error getting layout: %v", err)
	}

	utils.Logger.Printf("Layout retrieved\n")
	// printLayout(layout, 0)

	menuItems := FlattenLayout(layout)

	CreateMenuPanel(client, x, y, menuItems, 0)
}

func CreateMenuPanel(client *DBusMenuClient, x, y int, menuItems []menu.Item, parentId int32) {
	maxHorizontalLength, maxVerticalLength := menu.MaxLengthLabel(menuItems)+4, len(menuItems)
	utils.Logger.Printf("%d, %d\n", maxHorizontalLength, maxVerticalLength)

	kn := CreatePanel(x, y, maxHorizontalLength, maxVerticalLength)

	// Register with submenu manager
	sm := menu.GetManager()
	sm.AddPanel(kn, x, y)

	defer func() {
		sm.HandlePanelExit(kn)
	}()

	// Send initial menu to panel
	enc := cbor.NewEncoder(kn.Writer())
	msg := menu.Message{
		Type: menu.MsgMenuUpdate,
		Payload: menu.MessagePayload{
			Menu: menuItems,
		},
	}
	enc.Encode(msg)

	activeSubmenus := make(map[int32]*katnip.Panel)

	// Handle events from panel
	go func() {
		dec := cbor.NewDecoder(kn.Reader())
		for {
			var msg menu.Message
			if err := dec.Decode(&msg); err != nil {
				if err == io.EOF {
					break
				}
				utils.Logger.Printf("error decoding message from panel: %v", err)
				continue
			}

			switch msg.Type {
			case menu.MsgItemClicked:
				if msg.Payload.ItemId == -1 {
					utils.Logger.Printf("Clicked outside: %d", msg.Payload.ItemId)
					sm.CloseAllMenus()
					closeMsg := menu.Message{
						Type:    menu.MsgMenuClose,
						Payload: menu.MessagePayload{},
					}
					enc.Encode(closeMsg)
					return
				}
				if msg.Payload.ItemId != 0 {
					utils.Logger.Printf("Item clicked: %d", msg.Payload.ItemId)

					err := client.SendEvent(msg.Payload.ItemId, "clicked", "")
					if err != nil {
						utils.Logger.Printf("error sending clicked event: %v", err)
					}

					sm.CloseAllMenus()
					closeMsg := menu.Message{
						Type:    menu.MsgMenuClose,
						Payload: menu.MessagePayload{},
					}
					enc.Encode(closeMsg)
					return
				}

			case menu.MsgItemHovered:
				if msg.Payload.ItemId != 0 {
					utils.Logger.Printf("Item hovered: %d", msg.Payload.ItemId)
					err := client.SendEvent(msg.Payload.ItemId, "hovered", "")
					if err != nil {
						utils.Logger.Printf("error sending hovered event: %v", err)
					}
				}
			case menu.MsgSubmenuCancelRequested:
				if msg.Payload.ItemId != 0 {
					utils.Logger.Printf("Submenu cancel requested: %d", msg.Payload.ItemId)

					if submenuPanel, exists := activeSubmenus[msg.Payload.ItemId]; exists {
						cbor.NewEncoder(submenuPanel.Writer()).Encode(menu.Message{Type: menu.MsgMenuClose})
						// submenuPanel.Stop()
						delete(activeSubmenus, msg.Payload.ItemId)
					}

					sm.CloseAllSubmenus()
				}
			case menu.MsgSubmenuRequested:
				if msg.Payload.ItemId != 0 {
					utils.Logger.Printf("Submenu requested: %d", msg.Payload.ItemId)

					for itemId, submenuPanel := range activeSubmenus {
						cbor.NewEncoder(submenuPanel.Writer()).Encode(menu.Message{Type: menu.MsgMenuClose})
						// submenuPanel.Stop()
						delete(activeSubmenus, itemId)
					}

					needUpdate, err := client.AboutToShow(msg.Payload.ItemId)
					if err != nil {
						utils.Logger.Printf("error calling AboutToShow: %v", err)
					}
					if needUpdate {
						// Refresh the layout if needed
						newLayout, err := client.GetLayoutForParent(parentId)
						if err != nil {
							utils.Logger.Printf("error refreshing layout: %v", err)
						} else {
							newMenuItems := FlattenLayout(newLayout)
							updateMsg := menu.Message{
								Type: menu.MsgMenuUpdate,
								Payload: menu.MessagePayload{
									Menu: newMenuItems,
								},
							}
							enc.Encode(updateMsg)
						}
					}

					// Get submenu layout and spawn new panel
					submenuLayout, err := client.GetLayoutForParent(msg.Payload.ItemId)
					if err != nil {
						utils.Logger.Printf("error getting submenu layout: %v", err)
					} else if len(submenuLayout.Children) > 0 {
						submenuItems := FlattenLayout(submenuLayout)
						if len(submenuItems) > 0 {
							sm.CloseAllSubmenus()
							submenuX := x + msg.Payload.X - int((msg.Payload.State.PPC.X*float64(menu.MaxLengthLabel(submenuItems)+4))/2)
							submenuY := y + int((float64(msg.Payload.Y)*msg.Payload.State.PPC.Y)/2)
							// Launch submenu panel recursively this time instead bruh
							go CreateMenuPanel(client, submenuX, submenuY, submenuItems, msg.Payload.ItemId)
						}
					}
				}
			}
		}
	}()

	// Listen for DBus signals for layout updates
	go func() {
		rule := fmt.Sprintf("type='signal',sender='%s',path='%s',interface='com.canonical.dbusmenu'", client.busname, client.path)
		client.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)

		ch := make(chan *dbus.Signal, 10)
		client.conn.Signal(ch)

		for signal := range ch {
			switch signal.Name {
			case "com.canonical.dbusmenu.LayoutUpdated":
				utils.Logger.Printf("Layout updated signal received for panel %d", parentId)
				// Refresh layout
				newLayout, err := client.GetLayoutForParent(parentId)
				if err != nil {
					utils.Logger.Printf("error refreshing layout after signal: %v", err)
				} else {
					newMenuItems := FlattenLayout(newLayout)
					updateMsg := menu.Message{
						Type: menu.MsgMenuUpdate,
						Payload: menu.MessagePayload{
							Menu: newMenuItems,
						},
					}
					enc.Encode(updateMsg)
				}
			case "com.canonical.dbusmenu.ItemsPropertiesUpdated":
				utils.Logger.Printf("Items properties updated signal received for panel %d", parentId)
				newLayout, err := client.GetLayoutForParent(parentId)
				if err != nil {
					utils.Logger.Printf("error refreshing layout after properties update: %v", err)
				} else {
					newMenuItems := FlattenLayout(newLayout)
					updateMsg := menu.Message{
						Type: menu.MsgMenuUpdate,
						Payload: menu.MessagePayload{
							Menu: newMenuItems,
						},
					}
					enc.Encode(updateMsg)
				}
			}
		}
	}()

	kn.Wait()
}

func printLayout(l Layout, indent int) {
	t, ok := l.Properties["icon-data"]
	if ok {
		l.Properties["icon-data"] = dbus.MakeVariant("omitted...")
	}
	fmt.Printf("%sId: %v\n%sProperties:%v\n%sLayout:\n\n", strings.Repeat(" ", indent*4), l.Id, strings.Repeat(" ", indent*4), l.Properties, strings.Repeat(" ", indent*4))

	if ok {
		l.Properties["icon-data"] = t
	}

	for _, li := range l.Children {
		printLayout(li, indent+1)
	}
}

func FlattenLayout(parent Layout) (items []menu.Item) {
	for _, layout := range parent.Children {
		item := menu.Item{Id: layout.Id}
		if itemType, ok := layout.Properties["type"]; ok {
			item.Type = itemType.Value().(string)
		} else {
			item.Type = menu.ItemStandard
		}
		if label, ok := layout.Properties["label"]; ok {
			item.Label = menu.ParseLabel(label.Value().(string))
		}
		if enabled, ok := layout.Properties["enabled"]; ok {
			item.Enabled = enabled.Value().(bool)
		} else {
			item.Enabled = true
		}
		if visible, ok := layout.Properties["visible"]; ok {
			item.Visible = visible.Value().(bool)
		} else {
			item.Visible = true
		}
		if iconName, ok := layout.Properties["icon-name"]; ok {
			item.IconName = iconName.Value().(string)
			var icon xdgicons.Icon
			if strings.HasSuffix(item.IconName, "-symbolic") {
				icon, _ = iconLookup.Lookup(item.IconName)
			} else {
				icon, _ = iconLookup.FindBestIcon([]string{item.IconName + "-symbolic", item.IconName}, 48, 2)
			}
			item.Icon = icon
		}
		if iconData, ok := layout.Properties["icon-data"]; ok {
			item.IconData = iconData.Value().([]byte)
		}
		if shortcut, ok := layout.Properties["shortcut"]; ok {
			item.Shortcut = shortcut.Value().([][]string)
		}
		if toggleType, ok := layout.Properties["toggle-type"]; ok {
			item.ToggleType = toggleType.Value().(string)
		}
		if toggleState, ok := layout.Properties["toggle-state"]; ok {
			item.ToggleState = toggleState.Value().(int32)
		} else {
			item.ToggleState = -1
		}
		if childrenDisplay, ok := layout.Properties["children-display"]; ok {
			childrenDisplay := childrenDisplay.Value().(string)
			item.HasChildren = childrenDisplay == "submenu"
		}

		items = append(items, item)
	}

	return items
}

func init() {
	katnip.RegisterFunc("leaf", tui.Leaf)
}

func CreatePanel(x, y, w, h int) *katnip.Panel {
	conf := katnip.Config{
		Position: katnip.Vector{X: x, Y: y},
		Size:     katnip.Vector{X: w, Y: h},
		Edge:     katnip.EdgeNone,
		Layer:    katnip.LayerTop,
		// FocusPolicy: katnip.FocusOnDemand,
		FocusPolicy: katnip.FocusExclusive,
		ConfigFile:  "NONE",
		KittyOverrides: []string{
			"font_size=12",
			"cursor_trail=0",
			"cursor_shape=beam",
			"cursor=#000000",
			"paste_actions=replace-dangerous-control-codes",
			"map kitty_mod+equal       no_op",
			"map kitty_mod+plus        no_op",
			"map kitty_mod+kp_add      no_op",
			"map cmd+plus              no_op",
			"map cmd+equal             no_op",
			"map shift+cmd+equal       no_op",
			"map kitty_mod+minus       no_op",
			"map kitty_mod+kp_subtract no_op",
			"map cmd+minus             no_op",
			"map shift+cmd+minus       no_op",
			"map kitty_mod+backspace   no_op",
			"map cmd+0                 no_op",
		},
	}

	kn := katnip.NewPanel("leaf", conf)
	utils.Logger.Printf("%s", kn.Cmd.String())
	kn.Start()

	return kn
}
