// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package i3

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
)

const (
	ipcMagic                      = "i3-ipc"
	I3_IPC_MESSAGE_TYPE_SUBSCRIBE = 2
	IPC_GET_WORKSPACES            = 1
	msgTypeGetTree                = 4

	maxBackoff = 30 * time.Second
	// dispatchTimeout bounds a fan-out send so one wedged consumer
	// cannot stall the whole event stream.
	dispatchTimeout = 5 * time.Second
)

// EventReconnect marks the synthetic events dispatched after the event
// socket reconnects; consumers refresh their cached state on any event,
// so these just need to flow through the normal channels.
const EventReconnect = "pawbar:reconnect"

type Service struct {
	mu        sync.RWMutex
	callbacks map[string][]chan<- interface{}
	running   bool
	stop      chan struct{}
}

type WinInfo struct {
	Class string `json:"class"`
	Title string `json:"title"`
}

type WsInfo struct {
	Focused    bool    `json:"focused"`
	WindowInfo WinInfo `json:"window_properties"`
}

type WsIdentity struct {
	Focused         bool     `json:"focused"`
	Urgent          bool     `json:"urgent"`
	ScratchpadState string   `json:"scratchpad_state"`
	Name            string   `json:"name"`
	Nodes           []WsInfo `json:"nodes"`
}

type I3Event struct {
	Change  string     `json:"change"`
	Current WsIdentity `json:"current"`
	Old     WsIdentity `json:"old"`
}

type Workspace struct {
	Id      int    `json:"num"`
	Name    string `json:"name"`
	Focused bool   `json:"focused"`
	Urgent  bool   `json:"urgent"`
}

type WindowProperties struct {
	Class string `json:"class"`
	Title string `json:"title"`
}

type I3Node struct {
	Focused          bool              `json:"focused"`
	Nodes            []I3Node          `json:"nodes"`
	FloatingNodes    []I3Node          `json:"floating_nodes"`
	WindowProperties *WindowProperties `json:"window_properties"`
	Name             string            `json:"name"`
	AppId            string            `json:"app_id"`
}

type Container struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

type I3WEvent struct {
	Change    string    `json:"change"`
	Container Container `json:"container"`
}

func (i *Service) Name() string { return "i3" }

func (i *Service) Start() error {
	if i.running {
		return nil
	}

	if os.Getenv("I3SOCK") == "" && os.Getenv("SWAYSOCK") == "" {
		return fmt.Errorf("i3 or sway is not running.")
	}

	i.callbacks = make(map[string][]chan<- interface{})
	i.stop = make(chan struct{})
	go i.run()
	i.running = true
	return nil
}

func (i *Service) Stop() error {
	if !i.running {
		return nil
	}
	i.running = false
	close(i.stop)
	return nil
}

func (i *Service) RegisterChannel(event string, ch chan<- interface{}) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.callbacks[event] = append(i.callbacks[event], ch)
}

// UnregisterChannel removes ch from every event it was registered for.
func (i *Service) UnregisterChannel(ch chan<- interface{}) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for ev, chans := range i.callbacks {
		i.callbacks[ev] = slices.DeleteFunc(chans, func(c chan<- interface{}) bool { return c == ch })
	}
}

func (i *Service) dispatch(event string, v interface{}) {
	i.mu.RLock()
	chans := slices.Clone(i.callbacks[event])
	i.mu.RUnlock()
	for _, ch := range chans {
		select {
		case ch <- v:
		case <-time.After(dispatchTimeout):
			logging.Log.Error().Msgf("i3: consumer stuck, dropping %q event", event)
		case <-i.stop:
			return
		}
	}
}

func connectToI3() (net.Conn, error) {
	sockPath := os.Getenv("I3SOCK")
	if sockPath == "" {
		sockPath = os.Getenv("SWAYSOCK")
	}
	if sockPath == "" {
		return nil, fmt.Errorf("neither I3SOCK nor SWAYSOCK is set")
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("error connecting to i3 socket: %v", err)
	}

	return conn, nil
}

func sendI3Message(conn net.Conn, messageType uint32, payload []byte) error {
	header := make([]byte, 14)
	copy(header[:6], []byte(ipcMagic))
	binary.LittleEndian.PutUint32(header[6:10], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[10:14], messageType)

	sendMsg := append(header, payload...)

	if _, err := conn.Write(sendMsg); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}
	return nil
}

func readI3Ack(conn net.Conn) (string, error) {
	const headerSize = 14
	ackHeader := make([]byte, headerSize)
	if _, err := io.ReadFull(conn, ackHeader); err != nil {
		return "", fmt.Errorf("error reading ack header: %v", err)
	}

	if string(ackHeader[0:6]) != "i3-ipc" {
		return "", fmt.Errorf("invalid magic in header: expected 'i3-ipc', got '%s'", string(ackHeader[0:6]))
	}

	ackLen := binary.LittleEndian.Uint32(ackHeader[6:10])
	ackPayload := make([]byte, ackLen)
	if _, err := io.ReadFull(conn, ackPayload); err != nil {
		return "", fmt.Errorf("error reading ack payload: %v", err)
	}

	return string(ackPayload), nil
}

func readResponse(conn net.Conn) (uint32, []byte, error) {
	responseHeader := make([]byte, 14)
	if _, err := io.ReadFull(conn, responseHeader); err != nil {
		return 13, nil, fmt.Errorf("error reading response header: %v", err)
	}
	if string(responseHeader[:6]) != "i3-ipc" {
		return 13, nil, fmt.Errorf("invalid response magic: expected '%s', got '%s'", ipcMagic, string(responseHeader[:6]))
	}

	payloadLength := binary.LittleEndian.Uint32(responseHeader[6:10])
	payloadData := make([]byte, payloadLength)

	responseType := binary.LittleEndian.Uint32(responseHeader[10:14])

	if _, err := io.ReadFull(conn, payloadData); err != nil {
		return 13, nil, fmt.Errorf("error reading payload data: %v", err)
	}

	return responseType, payloadData, nil
}

// run subscribes to the event stream and fans events out, reconnecting
// with backoff when the socket drops (compositor restart/reload).
func (i *Service) run() {
	defer logging.Recover("i3.run")

	backoff := time.Second
	first := true
	for {
		select {
		case <-i.stop:
			return
		default:
		}

		err := i.listen(&first)
		select {
		case <-i.stop:
			return
		default:
		}
		if err != nil {
			logging.Log.Error().Msgf("i3: event stream: %v (retry in %v)", err, backoff)
		} else {
			logging.Log.Warn().Msgf("i3: event socket closed; reconnecting in %v", backoff)
		}
		select {
		case <-i.stop:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// listen runs one subscribe-and-read session; it returns when the
// connection fails or the service stops.
func (i *Service) listen(first *bool) error {
	conn, err := connectToI3()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Unblock the blocking reads when the service stops.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-i.stop:
			conn.Close()
		case <-watchDone:
		}
	}()

	subscription := []string{"window", "workspace"}
	payload, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("marshaling subscription payload: %w", err)
	}

	if err := sendI3Message(conn, I3_IPC_MESSAGE_TYPE_SUBSCRIBE, payload); err != nil {
		return err
	}

	ack, err := readI3Ack(conn)
	if err != nil {
		return err
	}
	logging.Log.Debug().Msgf("i3: subscription acknowledgment: %s", ack)

	if !*first {
		logging.Log.Info().Msg("i3: event socket reconnected")
		i.dispatch("workspaces", I3Event{Change: EventReconnect})
		i.dispatch("activeWindow", I3WEvent{Change: EventReconnect})
	}
	*first = false

	for {
		eventType, eventPayload, err := readResponse(conn)
		if err != nil {
			return err
		}

		switch eventType {
		case 0x80000000:
			var event I3Event
			if err := json.Unmarshal(eventPayload, &event); err != nil {
				logging.Log.Error().Msgf("i3: unmarshaling event: %v", err)
				continue
			}
			i.dispatch("workspaces", event)
		case 0x80000003:
			var wevent I3WEvent
			if err := json.Unmarshal(eventPayload, &wevent); err != nil {
				logging.Log.Error().Msgf("i3: unmarshaling event: %v", err)
				continue
			}
			i.dispatch("activeWindow", wevent)
		}
	}
}

func GetWorkspaces() ([]Workspace, error) {
	conn, err := connectToI3()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := sendI3Message(conn, IPC_GET_WORKSPACES, []byte("")); err != nil {
		return nil, err
	}

	_, eventPayload, err := readResponse(conn)
	if err != nil {
		return nil, err
	}

	var workspaces []Workspace
	if err = json.Unmarshal(eventPayload, &workspaces); err != nil {
		return nil, fmt.Errorf("unmarshaling workspaces: %w", err)
	}

	return workspaces, nil
}

func GoToWorkspace(name string) {
	cmd := exec.Command("i3-msg", "workspace", name)
	if err := cmd.Run(); err != nil {
		logging.Log.Error().Msgf("i3: workspace switch: %v", err)
	}
}

func GetActiveWorkspace() (Workspace, error) {
	workspaces, err := GetWorkspaces()
	if err != nil {
		return Workspace{}, err
	}
	for _, ws := range workspaces {
		if ws.Focused {
			return ws, nil
		}
	}
	return Workspace{}, nil
}

var isSway = os.Getenv("SWAYSOCK") != ""

func GetTitleClass() (string, string) {
	conn, err := connectToI3()
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		return "", ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := sendI3Message(conn, msgTypeGetTree, []byte("")); err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		return "", ""
	}

	_, eventPayload, err := readResponse(conn)
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		return "", ""
	}

	var root I3Node
	if err := json.Unmarshal(eventPayload, &root); err != nil {
		logging.Log.Error().Msgf("i3: parsing tree: %v", err)
		return "", ""
	}

	var focusedProps *WindowProperties
	var appid, name string

	var findFocused func(n *I3Node)
	findFocused = func(n *I3Node) {
		if focusedProps != nil {
			return
		}
		if n.Focused && isSway {
			appid = n.AppId
			name = n.Name
			return
		}

		if n.Focused && n.WindowProperties != nil {
			focusedProps = n.WindowProperties
			return
		}
		for i := range n.Nodes {
			findFocused(&n.Nodes[i])
		}
		for i := range n.FloatingNodes {
			findFocused(&n.FloatingNodes[i])
		}
	}
	findFocused(&root)

	if isSway {
		return appid, name
	}

	if focusedProps == nil {
		return "", ""
	}

	return focusedProps.Class, focusedProps.Title
}
