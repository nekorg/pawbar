// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package hypr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/nekorg/pawbar/internal/services"
)

func Register() (*Service, bool) {
	if s, ok := services.Ensure("hypr", func() services.Service { return &Service{} }).(*Service); ok {
		return s, true
	}
	return nil, false
}

type Service struct {
	callbacks map[string][]chan<- HyprEvent
	running   bool
}

func (h *Service) Name() string { return "hypr" }

func (h *Service) Start() error {
	if h.running {
		return nil
	}

	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return fmt.Errorf("Hyprland is not running.")
	}

	h.callbacks = make(map[string][]chan<- HyprEvent)
	go h.run()
	h.running = true
	return nil
}

func (h *Service) Stop() error {
	return nil
}

func (h *Service) RegisterChannel(event string, ch chan<- HyprEvent) {
	h.callbacks[event] = append(h.callbacks[event], ch)
}

func (h *Service) run() {
	_, sockaddr2 := GetHyprSocketAddrs()

	sock2, err := net.Dial("unix", sockaddr2)
	if err != nil {
		panic(err)
	}
	defer sock2.Close()

	scanner := bufio.NewScanner(sock2)
	for scanner.Scan() {
		e := NewHyprEvent(scanner.Text())
		c, ok := h.callbacks[e.Event]
		if ok {
			for _, ch := range c {
				ch <- e
			}
		}
	}
}

func GetHyprSocketAddrs() (string, string) {
	instance_signature := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	runtime_dir := os.Getenv("XDG_RUNTIME_DIR")
	socket_addr := path.Join(runtime_dir, "/hypr", instance_signature)

	return path.Join(socket_addr, "/.socket.sock"), path.Join(socket_addr, "/.socket2.sock")
}

type HyprEvent struct {
	Event string
	Data  string
}

func NewHyprEvent(s string) HyprEvent {
	e, d, _ := strings.Cut(s, ">>")
	return HyprEvent{e, strings.Trim(d, " \n")}
}

type Workspace struct {
	Id              int    `json:"id"`
	Name            string `json:"name"`
	Monitor         string `json:"monitor"`
	MonitorID       int    `json:"monitorID"`
	Windows         int    `json:"windows"`
	Hasfullscreen   bool   `json:"hasfullscreen"`
	Lastwindow      string `json:"lastwindow"`
	Lastwindowtitle string `json:"lastwindowtitle"`
}

type hyprVersion struct {
	Version string `json:"version"`
}

func getHyprVersion() string {
	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		panic(err)
	}
	defer sock.Close()

	sock.Write([]byte("-j/version"))
	var o hyprVersion

	err = json.NewDecoder(sock).Decode(&o)
	if err != nil {
		panic(err)
	}
	return o.Version
}

func isHyprVersionLessThan(version string) bool {
	return compareVersion(getHyprVersion(), version) < 0
}

func compareVersion(a, b string) int {
	ap := parseVersion(a)
	bp := parseVersion(b)

	for i := 0; i < len(ap) || i < len(bp); i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}

		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}

	return 0
}

func parseVersion(version string) []int {
	version = strings.TrimPrefix(version, "v")
	version = strings.Split(version, "-")[0]
	version = strings.Split(version, "+")[0]

	parts := strings.Split(version, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		parsed = append(parsed, n)
	}
	return parsed
}

func GetWorkspaces() []Workspace {
	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		panic(err)
	}
	defer sock.Close()
	scanner := json.NewDecoder(sock)

	sock.Write([]byte("-j/workspaces"))
	var o []Workspace

	err = scanner.Decode(&o)
	if err != nil {
		panic(err)
	}
	return o
}

func GetActiveWorkspace() Workspace {
	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		panic(err)
	}
	defer sock.Close()
	scanner := json.NewDecoder(sock)

	sock.Write([]byte("-j/activeworkspace"))
	var o Workspace

	err = scanner.Decode(&o)
	if err != nil {
		panic(err)
	}
	return o
}

type ClientWS struct {
	Id   int
	Name string
}

type Client struct {
	Address          string      `json:"address"`
	Mapped           bool        `json:"mapped"`
	Hidden           bool        `json:"hidden"`
	At               []int       `json:"at"`
	Size             []int       `json:"size"`
	Workspace        ClientWS    `json:"workspace"`
	Floating         bool        `json:"floating"`
	Pseudo           bool        `json:"pseudo"`
	Monitor          int         `json:"monitor"`
	Class            string      `json:"class"`
	Title            string      `json:"title"`
	InitialClass     string      `json:"initialClass"`
	InitialTitle     string      `json:"initialTitle"`
	Pid              int         `json:"pid"`
	Xwayland         bool        `json:"xwayland"`
	Pinned           bool        `json:"pinned"`
	Fullscreen       int         `json:"fullscreen"`
	FullscreenClient int         `json:"fullscreenClient"`
	Grouped          interface{} `json:"grouped"`
	Tags             interface{} `json:"tags"`
	Swallowing       string      `json:"swallowing"`
	FocusHistoryID   int         `json:"focusHistoryID"`
	InhibitingIdle   bool        `json:"inhibitingIdle"`
}

func GetClients() []Client {
	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		panic(err)
	}
	defer sock.Close()
	scanner := json.NewDecoder(sock)

	sock.Write([]byte("-j/clients"))
	var o []Client

	err = scanner.Decode(&o)
	if err != nil {
		panic(err)
	}
	return o
}

func GoToWorkspace(name string) {
	command := "/dispatch hl.dsp.focus({ workspace = \"" + name + "\" })"
	if isHyprVersionLessThan("0.55") {
		command = "/dispatch workspace " + name
	}

	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		panic(err)
	}
	defer sock.Close()

	sock.Write([]byte(command))
}
