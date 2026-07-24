// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sni

import (
	"fmt"
	"image"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
	"github.com/nekorg/pawbar/internal/logging"
)

const (
	ifaceWatcher = "org.kde.StatusNotifierWatcher"
	ifaceHost    = "org.kde.StatusNotifierHost"
	ifaceItem    = "org.kde.StatusNotifierItem"

	nameWatcher = "org.kde.StatusNotifierWatcher"
	pathWatcher = dbus.ObjectPath("/StatusNotifierWatcher")
	pathHost    = dbus.ObjectPath("/StatusNotifierHost")
)

type EventKind int

const (
	ItemAdded EventKind = iota
	ItemRemoved
	ItemChanged
)

type Event struct {
	Kind EventKind
	ID   string // stable key "<busname><path>"
	Item Item
}

type Category string

const (
	CatAppStatus  Category = "ApplicationStatus"
	CatComm       Category = "Communications"
	CatSysService Category = "SystemServices"
	CatHardware   Category = "Hardware"
)

type Item struct {
	BusName      string
	Path         dbus.ObjectPath
	Title        string
	Id           string
	Status       string // Passive/Active/NeedsAttention
	Category     Category
	IconName     string
	OverlayName  string
	Attention    string
	MenuPath     dbus.ObjectPath
	IconThemeDir string
	// IconPixmap is the item's best embedded icon frame, decoded from the
	// SNI IconPixmap property. It is a fallback for items that ship no
	// themed IconName (e.g. many Electron apps). Nil when absent.
	IconPixmap image.Image
}

type Service struct {
	conn    *dbus.Conn
	watcher dbus.BusObject
	owned   bool

	mu        sync.RWMutex
	items     map[string]*Item
	listeners []chan<- Event

	running bool
	stop    chan struct{}
	pending map[dbus.ObjectPath]struct{}

	// watchers holds the per-item stop channels for the property and
	// bus monitor goroutines, so removing an item tears them down.
	watchMu  sync.Mutex
	watchers map[string]chan struct{}

	// Properties when acting as watcher
	props *prop.Properties
	hosts map[string]bool // track registered hosts

	// For handling watcher transitions
	watcherOwner string // current watcher bus name
	hostName     string // our host registration name

	// Signal broadcasting
	signalBroadcaster *signalBroadcaster
}

// signalBroadcaster distributes dbus signals to multiple listeners
type signalBroadcaster struct {
	mu        sync.RWMutex
	listeners map[int]chan *dbus.Signal
	nextID    int
	stop      chan struct{}
}

func newSignalBroadcaster(conn *dbus.Conn) *signalBroadcaster {
	sb := &signalBroadcaster{
		listeners: make(map[int]chan *dbus.Signal),
		stop:      make(chan struct{}),
	}

	// Single signal channel from dbus
	mainCh := make(chan *dbus.Signal, 128)
	conn.Signal(mainCh)

	go func() {
		defer logging.Recover("sni.broadcaster")
		for {
			select {
			case <-sb.stop:
				return
			case sig, ok := <-mainCh:
				if !ok {
					// The bus connection closed; without this check the
					// closed channel would spin this loop at 100% CPU.
					logging.Log.Warn().Msg("sni: session bus signal channel closed")
					return
				}
				if sig == nil {
					continue
				}

				// Broadcast to all listeners
				sb.mu.RLock()
				for _, ch := range sb.listeners {
					select {
					case ch <- sig:
					default:
						// Channel full, skip
					}
				}
				sb.mu.RUnlock()
			}
		}
	}()

	return sb
}

func (sb *signalBroadcaster) Subscribe() (int, <-chan *dbus.Signal) {
	ch := make(chan *dbus.Signal, 64)

	sb.mu.Lock()
	id := sb.nextID
	sb.nextID++
	sb.listeners[id] = ch
	sb.mu.Unlock()

	return id, ch
}

func (sb *signalBroadcaster) Unsubscribe(id int) {
	sb.mu.Lock()
	if ch, ok := sb.listeners[id]; ok {
		close(ch)
		delete(sb.listeners, id)
	}
	sb.mu.Unlock()
}

func (sb *signalBroadcaster) Close() {
	close(sb.stop)
	sb.mu.Lock()
	for _, ch := range sb.listeners {
		close(ch)
	}
	sb.listeners = make(map[int]chan *dbus.Signal)
	sb.mu.Unlock()
}

func (s *Service) Name() string { return "sni" }

func (s *Service) IssueListener() <-chan Event {
	ch := make(chan Event, 16)
	s.mu.Lock()
	s.listeners = append(s.listeners, ch)
	s.mu.Unlock()
	return ch
}

func (s *Service) Start() error {
	if s.running {
		return nil
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	s.conn = conn
	s.items = make(map[string]*Item)
	s.hosts = make(map[string]bool)
	s.stop = make(chan struct{})
	s.pending = make(map[dbus.ObjectPath]struct{})
	s.watchers = make(map[string]chan struct{})

	// Create signal broadcaster
	s.signalBroadcaster = newSignalBroadcaster(conn)

	// Try to bind/attach to watcher
	s.owned, err = s.ensureWatcher()
	if err != nil {
		return err
	}

	s.watcher = s.conn.Object(nameWatcher, pathWatcher)

	// Register as a Host
	if err := s.registerHost(); err != nil {
		logging.Log.Warn().Msgf("sni: host registration failed: %v", err)
	}

	// Bootstrap: fetch current RegisteredStatusNotifierItems
	s.bootstrapItems()

	// Listen for signals
	go s.loop()

	s.running = true
	return nil
}

func (s *Service) Stop() error {
	if !s.running {
		return nil
	}

	close(s.stop)

	if s.signalBroadcaster != nil {
		s.signalBroadcaster.Close()
	}

	if s.owned {
		if _, err := s.conn.ReleaseName(nameWatcher); err != nil {
			return fmt.Errorf("releasing %q: %w", nameWatcher, err)
		}
	}

	// Unregister our host name
	if s.hostName != "" {
		s.conn.ReleaseName(s.hostName)
	}

	s.conn.Close()
	s.running = false
	return nil
}

func (s *Service) ensureWatcher() (owned bool, err error) {
	reply, reqErr := s.conn.RequestName(nameWatcher, dbus.NameFlagDoNotQueue)

	switch {
	case reqErr != nil:
		return false, reqErr
	case reply == dbus.RequestNameReplyPrimaryOwner:
		logging.Log.Debug().Msg("sni: acting as StatusNotifierWatcher")
		if err := s.exportWatcher(); err != nil {
			return false, fmt.Errorf("exporting watcher: %w", err)
		}
		s.watcherOwner = s.conn.Names()[0] // our unique name
		return true, nil
	case reply == dbus.RequestNameReplyInQueue:
		return false, fmt.Errorf("sni: watcher name queued unexpectedly")
	default:
		logging.Log.Debug().Msg("sni: external watcher detected")
		// Get the current owner
		var owner string
		if err := s.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, nameWatcher).Store(&owner); err == nil {
			s.watcherOwner = owner
		}
		return false, nil
	}
}

// Attempt to take over as watcher when the current one dies
func (s *Service) attemptWatcherTakeover() {
	s.mu.Lock()
	if s.owned {
		s.mu.Unlock()
		return // Already the watcher
	}
	s.mu.Unlock()

	logging.Log.Debug().Msg("sni: attempting to take over as watcher")

	// Try to claim the watcher name
	reply, err := s.conn.RequestName(nameWatcher, dbus.NameFlagDoNotQueue)
	if err != nil {
		logging.Log.Warn().Msgf("sni: takeover failed: %v", err)
		return
	}

	if reply != dbus.RequestNameReplyPrimaryOwner {
		logging.Log.Debug().Msg("sni: another watcher took over first")
		// Update our watcher reference
		var owner string
		if err := s.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, nameWatcher).Store(&owner); err == nil {
			s.mu.Lock()
			s.watcherOwner = owner
			s.mu.Unlock()
		}
		return
	}

	// We got it! Transition to being the watcher
	logging.Log.Debug().Msg("sni: successfully became the watcher")

	s.mu.Lock()
	s.owned = true
	s.watcherOwner = s.conn.Names()[0]

	// Preserve existing items (they're still valid)
	existingItems := make([]*Item, 0, len(s.items))
	for _, v := range s.items {
		existingItems = append(existingItems, v)
	}
	s.mu.Unlock()

	// Export watcher interface
	if err := s.exportWatcher(); err != nil {
		logging.Log.Warn().Msgf("sni: failed to export watcher interface: %v", err)
		s.conn.ReleaseName(nameWatcher)
		s.mu.Lock()
		s.owned = false
		s.mu.Unlock()
		return
	}

	// Re-register ourselves as a host (with the new watcher, which is us)
	s.mu.Lock()
	if s.hostName != "" {
		s.hosts[s.hostName] = true
	}
	s.mu.Unlock()

	// Restore items to properties
	if s.props != nil {
		s.props.SetMust(ifaceWatcher, "RegisteredStatusNotifierItems", s.getRegisteredItems())
		s.props.SetMust(ifaceWatcher, "IsStatusNotifierHostRegistered", s.isAnyHostRegistered())
	}

	// Start monitoring all existing items since we're now the watcher,
	// sharing each item's existing teardown channel.
	for _, it := range existingItems {
		key := it.BusName + string(it.Path)
		s.watchMu.Lock()
		done, ok := s.watchers[key]
		if !ok {
			done = make(chan struct{})
			s.watchers[key] = done
		}
		s.watchMu.Unlock()
		go s.monitorItemBus(it, done)
	}

	logging.Log.Debug().Msgf("sni: inherited %d items from previous watcher", len(existingItems))
}

// Watcher implementation
type watcherServer struct {
	s *Service
}

func (w *watcherServer) RegisterStatusNotifierItem(sender dbus.Sender, service string) *dbus.Error {
	// Parse the service argument
	bus, path := parseItemArg(service)

	// If bus is empty, use the sender's bus name
	if bus == "" {
		bus = string(sender)
	}

	// If still no bus (shouldn't happen), error out
	if bus == "" {
		return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
			[]interface{}{"No bus name provided"})
	}

	if path == "" {
		path = "/StatusNotifierItem"
	}

	// IMPORTANT: Store items by their original bus name, not resolved unique names
	// This ensures consistent keying
	logging.Log.Debug().Msgf("sni: registering item %s%s (sender: %s)", bus, path, sender)

	w.s.trackItem(bus, dbus.ObjectPath(path))

	// Emit signal with the full key
	key := bus + path
	w.s.conn.Emit(pathWatcher, sigItemRegistered, key)

	return nil
}

func (w *watcherServer) RegisterStatusNotifierHost(sender dbus.Sender, service string) *dbus.Error {
	logging.Log.Debug().Msgf("sni: registering host %s", service)

	w.s.mu.Lock()
	w.s.hosts[service] = true
	w.s.mu.Unlock()

	// Update property
	w.s.props.SetMust(ifaceWatcher, "IsStatusNotifierHostRegistered", w.s.isAnyHostRegistered())

	// Emit signal
	w.s.conn.Emit(pathWatcher, sigHostRegistered)

	return nil
}

const (
	sigItemRegistered   = ifaceWatcher + ".StatusNotifierItemRegistered"
	sigItemUnregistered = ifaceWatcher + ".StatusNotifierItemUnregistered"
	sigHostRegistered   = ifaceWatcher + ".StatusNotifierHostRegistered"
)

func (s *Service) exportWatcher() error {
	// Create properties map
	propsSpec := map[string]map[string]*prop.Prop{
		ifaceWatcher: {
			"RegisteredStatusNotifierItems": {
				Value:    s.getRegisteredItems(),
				Writable: false,
				Emit:     prop.EmitTrue,
				Callback: func(c *prop.Change) *dbus.Error {
					c.Value = s.getRegisteredItems()
					return nil
				},
			},
			"IsStatusNotifierHostRegistered": {
				Value:    s.isAnyHostRegistered(),
				Writable: false,
				Emit:     prop.EmitTrue,
				Callback: func(c *prop.Change) *dbus.Error {
					c.Value = s.isAnyHostRegistered()
					return nil
				},
			},
			"ProtocolVersion": {
				Value:    int32(0),
				Writable: false,
				Emit:     prop.EmitFalse,
			},
		},
	}

	props, err := prop.Export(s.conn, pathWatcher, propsSpec)
	if err != nil {
		return err
	}
	s.props = props

	// Export the watcher methods
	ws := &watcherServer{s: s}
	if err := s.conn.Export(ws, pathWatcher, ifaceWatcher); err != nil {
		return err
	}

	// Export introspection
	introspectXML := `
	<node>
		<interface name="org.kde.StatusNotifierWatcher">
			<method name="RegisterStatusNotifierItem">
				<arg name="service" type="s" direction="in"/>
			</method>
			<method name="RegisterStatusNotifierHost">
				<arg name="service" type="s" direction="in"/>
			</method>
			<property name="RegisteredStatusNotifierItems" type="as" access="read"/>
			<property name="IsStatusNotifierHostRegistered" type="b" access="read"/>
			<property name="ProtocolVersion" type="i" access="read"/>
			<signal name="StatusNotifierItemRegistered">
				<arg name="service" type="s"/>
			</signal>
			<signal name="StatusNotifierItemUnregistered">
				<arg name="service" type="s"/>
			</signal>
			<signal name="StatusNotifierHostRegistered"/>
		</interface>
	</node>`

	if err := s.conn.Export(introspect.Introspectable(introspectXML), pathWatcher,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	rootXML := `
    <node>
        <node name="StatusNotifierWatcher"/>
        <node name="StatusNotifierHost"/>
    </node>`
	if err := s.conn.Export(
		introspect.Introspectable(rootXML),
		dbus.ObjectPath("/"),
		"org.freedesktop.DBus.Introspectable",
	); err != nil {
		return err
	}

	// (Optional) introspection for the host path too
	hostXML := `<node><interface name="org.kde.StatusNotifierHost"/></node>`
	if err := s.conn.Export(
		introspect.Introspectable(hostXML),
		pathHost,
		"org.freedesktop.DBus.Introspectable",
	); err != nil {
		logging.Log.Warn().Msgf("sni: exporting host introspection: %v", err)
	}

	return nil
}

func (s *Service) getRegisteredItems() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, it := range s.items {
		// Return in the format "busname/path"
		items = append(items, it.BusName+string(it.Path))
	}

	sort.Strings(items)
	return items
}

func (s *Service) isAnyHostRegistered() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, registered := range s.hosts {
		if registered {
			return true
		}
	}
	return false
}

func (s *Service) registerHost() error {
	s.hostName = fmt.Sprintf("org.kde.StatusNotifierHost-%d", os.Getpid())
	if _, err := s.conn.RequestName(s.hostName, dbus.NameFlagDoNotQueue); err != nil {
		logging.Log.Warn().Msgf("sni: requesting host name %s: %v", s.hostName, err)
	}
	if err := s.conn.Export(struct{}{}, pathHost, ifaceHost); err != nil {
		logging.Log.Warn().Msgf("sni: exporting host object: %v", err)
	}

	// Only register if we're not the watcher
	if !s.owned {
		call := s.watcher.Call(ifaceWatcher+".RegisterStatusNotifierHost", 0, s.hostName)
		return call.Err
	}

	// Register ourselves as a host and advertise it: appindicator
	// libraries refuse to register items (falling back to XEmbed)
	// while IsStatusNotifierHostRegistered reads false.
	s.mu.Lock()
	s.hosts[s.hostName] = true
	s.mu.Unlock()
	if s.props != nil {
		s.props.SetMust(ifaceWatcher, "IsStatusNotifierHostRegistered", s.isAnyHostRegistered())
	}
	s.conn.Emit(pathWatcher, sigHostRegistered)

	return nil
}

func (s *Service) bootstrapItems() {
	// If we own the watcher, nothing to bootstrap
	if s.owned {
		return
	}

	var items []string
	if err := s.watcher.Call("org.freedesktop.DBus.Properties.Get", 0,
		ifaceWatcher, "RegisteredStatusNotifierItems").Store(&items); err != nil {
		logging.Log.Warn().Msgf("sni: bootstrap failed: %v", err)
		return
	}

	for _, item := range items {
		bus, path := parseItemArg(item)
		if bus != "" && path != "" {
			s.trackItem(bus, dbus.ObjectPath(path))
		}
	}
}

func (s *Service) trackItem(bus string, path dbus.ObjectPath) {
	key := bus + string(path)

	logging.Log.Debug().Msgf("sni: trackItem called - bus:%s path:%s key:%s", bus, path, key)

	s.mu.Lock()
	if _, exists := s.items[key]; exists {
		s.mu.Unlock()
		logging.Log.Debug().Msgf("sni: item %s already tracked", key)
		return
	}

	it := &Item{BusName: bus, Path: path}
	s.items[key] = it
	logging.Log.Debug().Msgf("sni: added item %s to tracking", key)
	s.mu.Unlock()

	// Update property if we're the watcher
	if s.owned && s.props != nil {
		s.props.SetMust(ifaceWatcher, "RegisteredStatusNotifierItems", s.getRegisteredItems())
	}

	// Fetch properties
	s.refreshItem(it)

	done := make(chan struct{})
	s.watchMu.Lock()
	s.watchers[key] = done
	s.watchMu.Unlock()

	// Watch for changes
	go s.watchItemProps(it, done)

	// If we're the watcher, monitor this item's bus for disconnection
	if s.owned {
		go s.monitorItemBus(it, done)
	}

	s.emit(Event{Kind: ItemAdded, ID: key, Item: *it})
}

// stopWatchers tears down the goroutines watching a removed item.
func (s *Service) stopWatchers(key string) {
	s.watchMu.Lock()
	if done, ok := s.watchers[key]; ok {
		close(done)
		delete(s.watchers, key)
	}
	s.watchMu.Unlock()
}

func (s *Service) removeByPath(path dbus.ObjectPath) {
	s.mu.Lock()
	var removed []*Item
	for k, it := range s.items {
		if it.Path == path {
			delete(s.items, k)
			removed = append(removed, it)
		}
	}
	s.mu.Unlock()

	// Update property if we're the watcher. Must happen outside the
	// lock: getRegisteredItems re-locks.
	if s.owned && s.props != nil && len(removed) > 0 {
		s.props.SetMust(ifaceWatcher, "RegisteredStatusNotifierItems", s.getRegisteredItems())
	}

	// Emit events outside the lock to prevent deadlocks
	for _, it := range removed {
		key := it.BusName + string(it.Path)
		s.stopWatchers(key)

		// Emit unregistered signal if we're the watcher
		if s.owned {
			s.conn.Emit(pathWatcher, sigItemUnregistered, key)
		}

		s.emit(Event{Kind: ItemRemoved, ID: key, Item: *it})
	}
}

func (s *Service) removeByKey(key string) {
	logging.Log.Debug().Msgf("sni: removeByKey called for %s", key)

	s.mu.Lock()
	it, exists := s.items[key]
	if !exists {
		logging.Log.Debug().Msgf("sni: key %s not found in items map", key)
		// List all current keys for debugging
		var keys []string
		for k := range s.items {
			keys = append(keys, k)
		}
		logging.Log.Debug().Msgf("sni: current items: %v", keys)
		s.mu.Unlock()
		return
	}

	delete(s.items, key)
	logging.Log.Debug().Msgf("sni: removed item %s from map", key)
	wasOwned := s.owned
	s.mu.Unlock()

	s.stopWatchers(key)

	// Update property if we're the watcher. Must happen outside the
	// lock: getRegisteredItems re-locks.
	if wasOwned && s.props != nil {
		s.props.SetMust(ifaceWatcher, "RegisteredStatusNotifierItems", s.getRegisteredItems())
	}

	// Emit unregistered signal if we're the watcher (must be outside lock)
	if wasOwned {
		// Emit the signal that other hosts are listening for
		logging.Log.Debug().Msgf("sni: emitting StatusNotifierItemUnregistered for %s", key)
		if err := s.conn.Emit(pathWatcher, sigItemUnregistered, key); err != nil {
			logging.Log.Warn().Msgf("sni: failed to emit unregistered signal: %v", err)
		}
	}

	// Emit our internal event
	s.emit(Event{Kind: ItemRemoved, ID: key, Item: *it})
}

func (s *Service) purgeByBus(bus string) {
	s.mu.Lock()
	var removed []*Item
	for k, it := range s.items {
		if it.BusName == bus {
			delete(s.items, k)
			removed = append(removed, it)
		}
	}
	s.mu.Unlock()

	// Update property if we're the watcher. Must happen outside the
	// lock: getRegisteredItems re-locks.
	if s.owned && s.props != nil && len(removed) > 0 {
		s.props.SetMust(ifaceWatcher, "RegisteredStatusNotifierItems", s.getRegisteredItems())
	}

	// Emit signals for removed items outside the lock
	for _, it := range removed {
		key := it.BusName + string(it.Path)
		s.stopWatchers(key)

		// Emit unregistered signal if we're the watcher
		if s.owned {
			s.conn.Emit(pathWatcher, sigItemUnregistered, key)
		}

		s.emit(Event{Kind: ItemRemoved, ID: key, Item: *it})
	}
}

func parseItemArg(s string) (bus string, path string) {
	if s == "" {
		return "", ""
	}

	if strings.Contains(s, "/") {
		parts := strings.SplitN(s, "/", 2)
		return parts[0], "/" + parts[1]
	}

	// If it starts with '/', it's just a path
	if strings.HasPrefix(s, "/") {
		return "", s
	}

	// Otherwise it's just a bus name
	return s, ""
}

func (s *Service) loop() {
	// Subscribe to signals
	rule := fmt.Sprintf("type='signal',interface='%s'", ifaceWatcher)
	s.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)

	nameRule := "type='signal',interface='org.freedesktop.DBus',member='NameOwnerChanged'"
	s.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, nameRule)

	// Subscribe to broadcaster
	subID, sigch := s.signalBroadcaster.Subscribe()
	defer s.signalBroadcaster.Unsubscribe(subID)

	for {
		select {
		case <-s.stop:
			return
		case sig, ok := <-sigch:
			if !ok {
				return
			}
			if sig == nil {
				continue
			}

			switch sig.Name {
			case sigItemRegistered:
				// Only process if we're not the watcher (avoid loops)
				if s.owned {
					continue
				}

				if len(sig.Body) < 1 {
					continue
				}

				if service, ok := sig.Body[0].(string); ok {
					bus, path := parseItemArg(service)
					if bus != "" && path != "" {
						s.trackItem(bus, dbus.ObjectPath(path))
					}
				}

			case sigItemUnregistered:
				// Only process if we're not the watcher (avoid loops)
				if s.owned {
					continue
				}

				if len(sig.Body) >= 1 {
					if service, ok := sig.Body[0].(string); ok {
						// The service could be in various formats:
						// - "busname/path" (full key)
						// - "/path" (just path)
						// - "busname" (just bus)

						// First try as a direct key
						s.mu.RLock()
						_, hasKey := s.items[service]
						s.mu.RUnlock()

						if hasKey {
							s.removeByKey(service)
						} else {
							// Try parsing as bus/path
							bus, path := parseItemArg(service)
							if path != "" && bus != "" {
								key := bus + path
								s.removeByKey(key)
							} else if path != "" {
								// Just a path, remove all items with this path
								s.removeByPath(dbus.ObjectPath(path))
							} else if bus != "" {
								// Just a bus name, remove all items from this bus
								s.purgeByBus(bus)
							}
						}
					}
				}

			case "org.freedesktop.DBus.NameOwnerChanged":
				if len(sig.Body) >= 3 {
					name, _ := sig.Body[0].(string)
					old, _ := sig.Body[1].(string)
					newo, _ := sig.Body[2].(string)

					// Check if the watcher died
					if name == nameWatcher && old != "" && newo == "" {
						s.mu.RLock()
						wasOurWatcher := (old == s.watcherOwner)
						wasOwned := s.owned
						s.mu.RUnlock()

						if wasOurWatcher && !wasOwned {
							logging.Log.Debug().Msg("sni: external watcher disappeared")
							// Give a small delay for other services to claim it first
							time.Sleep(100 * time.Millisecond)
							s.attemptWatcherTakeover()
						}
						continue
					}

					// Check if a new watcher appeared
					if name == nameWatcher && old == "" && newo != "" {
						s.mu.Lock()
						if !s.owned {
							s.watcherOwner = newo
							logging.Log.Debug().Msgf("sni: new external watcher appeared: %s", newo)
							// Re-register as host with new watcher
							s.mu.Unlock()
							s.watcher = s.conn.Object(nameWatcher, pathWatcher)
							if s.hostName != "" {
								s.watcher.Call(ifaceWatcher+".RegisterStatusNotifierHost", 0, s.hostName)
							}
						} else {
							s.mu.Unlock()
						}
						continue
					}

					// When we're the watcher, the monitor goroutines handle item removal
					// When we're not the watcher, we need to handle it here
					s.mu.RLock()
					isOwned := s.owned
					s.mu.RUnlock()

					if !isOwned && old != "" && newo == "" {
						// A service disappeared and we're not the watcher
						removed := false

						s.mu.RLock()
						for _, it := range s.items {
							if it.BusName == old || it.BusName == name {
								removed = true
								break
							}
						}
						s.mu.RUnlock()

						if removed {
							logging.Log.Debug().Msgf("sni: service disappeared: %s (was %s)", name, old)
							s.purgeByBus(old)
							if name != "" && name != old {
								s.purgeByBus(name)
							}
						}

						// Check if it was a host
						s.mu.Lock()
						if _, wasHost := s.hosts[name]; wasHost {
							delete(s.hosts, name)
							s.mu.Unlock()

							if s.owned && s.props != nil {
								s.props.SetMust(ifaceWatcher, "IsStatusNotifierHostRegistered",
									s.isAnyHostRegistered())
							}
						} else {
							s.mu.Unlock()
						}
					}
				}
			}
		}
	}
}

func (s *Service) refreshItem(it *Item) {
	obj := s.conn.Object(it.BusName, it.Path)

	grab := func(p string, into *string) {
		var v dbus.Variant
		if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, ifaceItem, p).Store(&v); err == nil {
			if str, ok := v.Value().(string); ok {
				*into = str
			}
		}
	}

	var mp dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, ifaceItem, "Menu").Store(&mp); err == nil {
		if p, ok := mp.Value().(dbus.ObjectPath); ok {
			it.MenuPath = p
		}
	}

	grab("Id", &it.Id)
	grab("Title", &it.Title)
	grab("Status", &it.Status)
	grab("IconName", &it.IconName)
	grab("OverlayIconName", &it.OverlayName)
	grab("AttentionIconName", &it.Attention)
	grab("IconThemePath", &it.IconThemeDir)

	var pmv dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, ifaceItem, "IconPixmap").Store(&pmv); err == nil {
		if img := decodePixmap(pmv); img != nil {
			it.IconPixmap = img
		}
	}

	var catv dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, ifaceItem, "Category").Store(&catv); err == nil {
		if c, ok := catv.Value().(string); ok {
			it.Category = Category(c)
		}
	}
}

// decodePixmap parses the SNI IconPixmap property (signature a(iiay): an
// array of width/height/ARGB32-big-endian frames) and returns the largest
// frame as an image. Returns nil when no usable frame is present.
func decodePixmap(v dbus.Variant) image.Image {
	frames, ok := v.Value().([][]any)
	if !ok {
		return nil
	}
	var bw, bh int
	var best []byte
	for _, f := range frames {
		if len(f) != 3 {
			continue
		}
		w, _ := f[0].(int32)
		h, _ := f[1].(int32)
		data, _ := f[2].([]byte)
		if w <= 0 || h <= 0 || len(data) < int(w)*int(h)*4 {
			continue
		}
		if int(w)*int(h) > bw*bh {
			bw, bh, best = int(w), int(h), data
		}
	}
	if best == nil {
		return nil
	}
	// SNI pixmaps are ARGB32 in network byte order; NRGBA is
	// non-premultiplied like the wire data, so channels map straight over.
	img := image.NewNRGBA(image.Rect(0, 0, bw, bh))
	n := bw * bh * 4
	for i := 0; i+3 < n && i+3 < len(best); i += 4 {
		img.Pix[i] = best[i+1]   // R
		img.Pix[i+1] = best[i+2] // G
		img.Pix[i+2] = best[i+3] // B
		img.Pix[i+3] = best[i]   // A
	}
	return img
}

func (s *Service) watchItemProps(it *Item, done <-chan struct{}) {
	defer logging.Recover("sni.watchItemProps")
	rule := fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s',sender='%s'",
		it.Path, it.BusName)
	s.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)

	ch := make(chan *dbus.Signal, 16)
	s.conn.Signal(ch)
	defer func() {
		s.conn.RemoveSignal(ch)
		s.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, rule)
	}()

	for {
		select {
		case <-s.stop:
			return
		case <-done:
			return
		case sig := <-ch:
			if sig == nil || sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
				continue
			}

			// Verify it's for our item
			if sig.Path != it.Path || sig.Sender != it.BusName {
				continue
			}

			if len(sig.Body) < 2 {
				continue
			}

			iface, _ := sig.Body[0].(string)
			if iface != ifaceItem {
				continue
			}

			changed, _ := sig.Body[1].(map[string]dbus.Variant)
			s.applyChanges(it, changed)
		}
	}
}

// monitorItemBus watches for when an item's bus connection dies (only when we're the watcher)
func (s *Service) monitorItemBus(it *Item, done <-chan struct{}) {
	defer logging.Recover("sni.monitorItemBus")
	key := it.BusName + string(it.Path)
	logging.Log.Debug().Msgf("sni: starting monitor for item %s", key)

	// For unique names (:1.x), we can watch NameOwnerChanged
	// For well-known names, we need to resolve to unique name first
	busToWatch := it.BusName
	uniqueName := ""

	// If it's a well-known name, get the unique name
	if !strings.HasPrefix(busToWatch, ":") {
		var owner string
		err := s.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, busToWatch).Store(&owner)
		if err != nil {
			logging.Log.Warn().Msgf("sni: can't resolve owner for %s: %v", busToWatch, err)
			// Can't resolve, maybe it's already gone?
			s.removeByKey(key)
			return
		}
		uniqueName = owner
		logging.Log.Debug().Msgf("sni: resolved %s to unique name %s", busToWatch, uniqueName)
	} else {
		uniqueName = busToWatch
	}

	// Create a dedicated signal channel for this monitor
	ch := make(chan *dbus.Signal, 32)
	s.conn.Signal(ch)

	// Watch for both the well-known name and unique name
	rule1 := "type='signal',interface='org.freedesktop.DBus',member='NameOwnerChanged'"
	s.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule1)

	defer func() {
		s.conn.RemoveSignal(ch)
		s.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, rule1)
	}()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			logging.Log.Debug().Msgf("sni: stopping monitor for %s (service stopping)", key)
			return

		case <-done:
			return

		case <-ticker.C:
			// Periodic health check
			s.mu.RLock()
			_, stillTracked := s.items[key]
			s.mu.RUnlock()

			if !stillTracked {
				logging.Log.Debug().Msgf("sni: item %s no longer tracked, stopping monitor", key)
				return
			}

			if !s.itemStillExists(it) {
				logging.Log.Warn().Msgf("sni: item %s no longer exists (health check failed)", key)
				s.removeByKey(key)
				return
			}

		case sig := <-ch:
			if sig == nil {
				continue
			}

			if sig.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(sig.Body) >= 3 {
				name, _ := sig.Body[0].(string)
				old, _ := sig.Body[1].(string)
				newo, _ := sig.Body[2].(string)

				// Check if this is our monitored bus going away
				if old != "" && newo == "" {
					if name == uniqueName || name == it.BusName ||
						(uniqueName != "" && old == uniqueName) {
						logging.Log.Debug().Msgf("sni: detected disconnect - name:%s old:%s new:%s (monitoring %s/%s)",
							name, old, newo, it.BusName, uniqueName)
						s.removeByKey(key)
						return
					}
				}
			}
		}
	}
}

// itemStillExists checks if an item is still alive on the bus
func (s *Service) itemStillExists(it *Item) bool {
	// Try to ping the item by getting a property
	var v dbus.Variant
	err := s.conn.Object(it.BusName, it.Path).Call(
		"org.freedesktop.DBus.Properties.Get", 0, ifaceItem, "Id",
	).Store(&v)
	return err == nil
}

func (s *Service) applyChanges(it *Item, changed map[string]dbus.Variant) {
	s.mu.Lock()

	for k, v := range changed {
		switch k {
		case "Id":
			if str, ok := v.Value().(string); ok {
				it.Id = str
			}
		case "Title":
			if str, ok := v.Value().(string); ok {
				it.Title = str
			}
		case "Status":
			if str, ok := v.Value().(string); ok {
				it.Status = str
			}
		case "IconName":
			if str, ok := v.Value().(string); ok {
				it.IconName = str
			}
		case "OverlayIconName":
			if str, ok := v.Value().(string); ok {
				it.OverlayName = str
			}
		case "AttentionIconName":
			if str, ok := v.Value().(string); ok {
				it.Attention = str
			}
		case "IconThemePath":
			if str, ok := v.Value().(string); ok {
				it.IconThemeDir = str
			}
		case "IconPixmap":
			if img := decodePixmap(v); img != nil {
				it.IconPixmap = img
			}
		case "Menu":
			if p, ok := v.Value().(dbus.ObjectPath); ok {
				it.MenuPath = p
			}
		case "Category":
			if str, ok := v.Value().(string); ok {
				it.Category = Category(str)
			}
		}
	}

	key := it.BusName + string(it.Path)
	item := *it
	s.mu.Unlock()

	// Emit outside the lock: emit re-locks to snapshot listeners.
	s.emit(Event{Kind: ItemChanged, ID: key, Item: item})
}

func (s *Service) emit(ev Event) {
	// Debug logging
	switch ev.Kind {
	case ItemAdded:
		logging.Log.Debug().Msgf("sni: emitting ItemAdded for %s", ev.ID)
	case ItemRemoved:
		logging.Log.Debug().Msgf("sni: emitting ItemRemoved for %s", ev.ID)
	case ItemChanged:
		// Less verbose for changes
	}

	s.mu.RLock()
	listeners := make([]chan<- Event, len(s.listeners))
	copy(listeners, s.listeners)
	s.mu.RUnlock()

	for _, ch := range listeners {
		select {
		case ch <- ev:
		default:
			logging.Log.Warn().Msgf("sni: listener channel full, dropping event %v", ev.Kind)
		}
	}
}

// Public API methods
func (s *Service) Items() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, *it)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Id < out[j].Id
	})

	return out
}

func (s *Service) Activate(it Item, x, y int32) error {
	return s.conn.Object(it.BusName, it.Path).Call(ifaceItem+".Activate", 0, x, y).Err
}

func (s *Service) SecondaryActivate(it Item, x, y int32) error {
	return s.conn.Object(it.BusName, it.Path).Call(ifaceItem+".SecondaryActivate", 0, x, y).Err
}

func (s *Service) ContextMenu(it Item, x, y int32) error {
	return s.conn.Object(it.BusName, it.Path).Call(ifaceItem+".ContextMenu", 0, x, y).Err
}

func (s *Service) Scroll(it Item, delta int32, orientation string) error {
	return s.conn.Object(it.BusName, it.Path).Call(ifaceItem+".Scroll", 0, delta, orientation).Err
}
