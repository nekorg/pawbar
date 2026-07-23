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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
)

// EventReconnect is a synthetic event dispatched to every registered
// channel after the event socket reconnects. Consumers with cached state
// must refresh it: events were missed while disconnected.
const EventReconnect = "pawbar:reconnect"

const (
	requestTimeout = 5 * time.Second
	maxBackoff     = 30 * time.Second
	// dispatchTimeout bounds a fan-out send so one wedged consumer
	// cannot stall the whole event stream.
	dispatchTimeout = 5 * time.Second
)

type Service struct {
	mu        sync.RWMutex
	callbacks map[string][]chan<- HyprEvent
	running   bool
	stop      chan struct{}
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
	h.stop = make(chan struct{})
	go h.run()
	h.running = true
	return nil
}

func (h *Service) Stop() error {
	if !h.running {
		return nil
	}
	h.running = false
	close(h.stop)
	return nil
}

func (h *Service) RegisterChannel(event string, ch chan<- HyprEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callbacks[event] = append(h.callbacks[event], ch)
}

// UnregisterChannel removes ch from every event it was registered for.
func (h *Service) UnregisterChannel(ch chan<- HyprEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ev, chans := range h.callbacks {
		h.callbacks[ev] = slices.DeleteFunc(chans, func(c chan<- HyprEvent) bool { return c == ch })
	}
}

// run reads the socket2 event stream and fans events out, reconnecting
// with backoff when the socket drops (Hyprland reload/restart).
func (h *Service) run() {
	defer logging.Recover("hypr.run")
	_, sockaddr2 := GetHyprSocketAddrs()

	backoff := time.Second
	first := true
	for {
		select {
		case <-h.stop:
			return
		default:
		}

		sock, err := net.Dial("unix", sockaddr2)
		if err != nil {
			logging.Log.Error().Msgf("hypr: event socket dial: %v (retry in %v)", err, backoff)
			select {
			case <-h.stop:
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		if !first {
			logging.Log.Info().Msg("hypr: event socket reconnected")
			h.dispatch(HyprEvent{Event: EventReconnect})
		}
		first = false

		// Unblock the scanner when the service stops.
		watchDone := make(chan struct{})
		go func() {
			select {
			case <-h.stop:
				sock.Close()
			case <-watchDone:
			}
		}()

		scanner := bufio.NewScanner(sock)
		// Events carry window titles; the default 64KiB token cap
		// would kill the stream on oversized lines.
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			h.dispatch(NewHyprEvent(scanner.Text()))
		}
		close(watchDone)
		sock.Close()

		select {
		case <-h.stop:
			return
		default:
		}
		if err := scanner.Err(); err != nil {
			logging.Log.Warn().Msgf("hypr: event socket read: %v; reconnecting", err)
		} else {
			logging.Log.Warn().Msg("hypr: event socket closed; reconnecting")
		}
	}
}

func (h *Service) dispatch(e HyprEvent) {
	h.mu.RLock()
	chans := slices.Clone(h.callbacks[e.Event])
	h.mu.RUnlock()
	for _, ch := range chans {
		select {
		case ch <- e:
		case <-time.After(dispatchTimeout):
			logging.Log.Error().Msgf("hypr: consumer stuck, dropping %q event", e.Event)
		case <-h.stop:
			return
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

// request performs one socket1 command, decoding the JSON response into
// v when v is non-nil.
func request(command string, v any) error {
	sockaddr1, _ := GetHyprSocketAddrs()
	sock, err := net.Dial("unix", sockaddr1)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer sock.Close()
	sock.SetDeadline(time.Now().Add(requestTimeout))

	if _, err := sock.Write([]byte(command)); err != nil {
		return fmt.Errorf("write %q: %w", command, err)
	}
	if v == nil {
		return nil
	}
	if err := json.NewDecoder(sock).Decode(v); err != nil {
		return fmt.Errorf("decode %q response: %w", command, err)
	}
	return nil
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

var (
	versionMu     sync.Mutex
	cachedVersion string
)

func getHyprVersion() (string, error) {
	versionMu.Lock()
	defer versionMu.Unlock()
	if cachedVersion != "" {
		return cachedVersion, nil
	}
	var o hyprVersion
	if err := request("-j/version", &o); err != nil {
		return "", err
	}
	cachedVersion = o.Version
	return o.Version, nil
}

func isHyprVersionLessThan(version string) bool {
	v, err := getHyprVersion()
	if err != nil {
		logging.Log.Warn().Msgf("hypr: version query: %v; assuming current dispatch syntax", err)
		return false
	}
	return compareVersion(v, version) < 0
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

func GetWorkspaces() ([]Workspace, error) {
	var o []Workspace
	if err := request("-j/workspaces", &o); err != nil {
		return nil, err
	}
	return o, nil
}

func GetActiveWorkspace() (Workspace, error) {
	var o Workspace
	if err := request("-j/activeworkspace", &o); err != nil {
		return Workspace{}, err
	}
	return o, nil
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

func GetClients() ([]Client, error) {
	var o []Client
	if err := request("-j/clients", &o); err != nil {
		return nil, err
	}
	return o, nil
}

func GoToWorkspace(name string) error {
	command := "/dispatch hl.dsp.focus({ workspace = \"" + name + "\" })"
	if isHyprVersionLessThan("0.55") {
		command = "/dispatch workspace " + name
	}
	return request(command, nil)
}
