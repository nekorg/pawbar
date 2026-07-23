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

	"github.com/nekorg/pawbar/internal/logging"
)

const (
	ipcMagic                      = "i3-ipc"
	I3_IPC_MESSAGE_TYPE_SUBSCRIBE = 2
	IPC_GET_WORKSPACES            = 1
	msgTypeGetTree                = 4
)

var (
	event  I3Event
	wevent I3WEvent
)

type Service struct {
	callbacks map[string][]chan<- interface{}
	running   bool
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

	if os.Getenv("I3SOCK") == "" {
		return fmt.Errorf("i3 or sway is not running.")
	}

	i.callbacks = make(map[string][]chan<- interface{})
	go i.sockMsg()
	i.running = true
	return nil
}

func (i *Service) Stop() error {
	return nil
}

func (i *Service) RegisterChannel(event string, ch chan<- interface{}) {
	i.callbacks[event] = append(i.callbacks[event], ch)
}

func connectToI3() (net.Conn, error) {
	sockPath := os.Getenv("I3SOCK")
	if sockPath == "" {
		return nil, fmt.Errorf("I3SOCK environment variable is not set")
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

func (i *Service) sockMsg() {
	conn, err := connectToI3()
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}
	defer conn.Close()

	subscription := []string{"window", "workspace"}
	payload, err := json.Marshal(subscription)
	if err != nil {
		logging.Log.Warn().Msgf("Error marshaling subscription payload: %v", err)
		os.Exit(1)
	}

	if err := sendI3Message(conn, I3_IPC_MESSAGE_TYPE_SUBSCRIBE, payload); err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}

	ack, err := readI3Ack(conn)
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}

	logging.Log.Debug().Msgf("i3: subscription acknowledgment: %s", ack)

	for {
		eventType, eventPayload, err := readResponse(conn)
		if err != nil {
			logging.Log.Error().Msgf("i3: reading response: %v", err)
			break
		}

		switch eventType {
		case 0x80000000:
			if err := json.Unmarshal(eventPayload, &event); err != nil {
				logging.Log.Error().Msgf("i3: unmarshaling event: %v", err)
				continue
			}

			if chans, ok := i.callbacks["workspaces"]; ok {
				for _, ch := range chans {
					ch <- event
				}
			}
		case 0x80000003:
			if err := json.Unmarshal(eventPayload, &wevent); err != nil {
				logging.Log.Error().Msgf("i3: unmarshaling event: %v", err)
				continue
			}

			if chans, ok := i.callbacks["activeWindow"]; ok {
				for _, ch := range chans {
					ch <- wevent
				}
			}
		}
	}
}

func GetWorkspaces() []Workspace {
	conn, err := connectToI3()
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}
	defer conn.Close()

	payload := []byte("")

	if err := sendI3Message(conn, IPC_GET_WORKSPACES, payload); err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}

	eventType, eventPayload, err := readResponse(conn)
	logging.Log.Debug().Msgf("i3: event of type: %d", eventType)
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		return nil
	}

	var workspaces []Workspace
	if err = json.Unmarshal(eventPayload, &workspaces); err != nil {
		logging.Log.Error().Msgf("i3: unmarshaling JSON: %v", err)
		return nil
	}

	return workspaces
}

func GoToWorkspace(name string) {
	cmd := exec.Command("i3-msg", "workspace", name)
	if err := cmd.Run(); err != nil {
		logging.Log.Warn().Msgf("Error executing command: %v", err)
		os.Exit(1)
	}
}

func GetActiveWorkspace() Workspace {
	workspaces := GetWorkspaces()
	for _, ws := range workspaces {
		if ws.Focused {
			return ws
		}
	}
	return Workspace{}
}

var isSway = os.Getenv("SWAYSOCK") != ""

func GetTitleClass() (string, string) {
	conn, err := connectToI3()
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}
	defer conn.Close()

	payload := []byte("")

	if err := sendI3Message(conn, msgTypeGetTree, payload); err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		os.Exit(1)
	}

	eventType, eventPayload, err := readResponse(conn)
	logging.Log.Debug().Msgf("i3: event of type: %d", eventType)
	if err != nil {
		logging.Log.Error().Msgf("i3: %v", err)
		return "", ""
	}

	var root I3Node
	if err := json.Unmarshal(eventPayload, &root); err != nil {
		logging.Log.Warn().Msgf("Failed to parse JSON: %v", err)
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
