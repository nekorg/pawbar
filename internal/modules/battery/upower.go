// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package battery

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/logging"
)

const (
	UP_NAME                = "org.freedesktop.UPower"
	UP_DISPLAY_DEVICE_PATH = "/org/freedesktop/UPower/devices/DisplayDevice"
	DBUS_PROPS_IFACE       = "org.freedesktop.DBus.Properties"
	DBUS_PROPS_GETALL      = "org.freedesktop.DBus.Properties.GetAll"
	UP_IFACE               = "org.freedesktop.UPower.Device"
	DBUS_PROPS_SIGNAL      = "org.freedesktop.DBus.Properties.PropertiesChanged"
)

const (
	DeviceUnknown uint32 = iota
	DeviceLinePower
	DeviceBattery
	DeviceUps
	DeviceMonitor
	DeviceMouse
	DeviceKeyboard
	DevicePda
	DevicePhone
	DeviceMediaPlayer
	DeviceTablet
	DeviceComputer
	DeviceGamingInput
	DevicePen
	DeviceTouchpad
	DeviceModem
	DeviceNetwork
	DeviceHeadset
	DeviceSpeakers
	DeviceHeadphones
	DeviceVideo
	DeviceOtherAudio
	DeviceRemoteControl
	DevicePrinter
	DeviceScanner
	DeviceCamera
	DeviceWearable
	DeviceToy
	DeviceBluetoothGeneric
)

const (
	StateUnknown uint32 = iota
	StateCharging
	StateDischarging
	StateEmpty
	StateFullyCharged
	StatePendingCharge
	StatePendingDischarge
)

type UPowerDevice struct {
	NativePath               string  `dbus:"NativePath"`
	Vendor                   string  `dbus:"Vendor"`
	Model                    string  `dbus:"Model"`
	Serial                   string  `dbus:"Serial"`
	UpdateTime               uint64  `dbus:"UpdateTime"`
	Type                     uint32  `dbus:"Type"`
	PowerSupply              bool    `dbus:"PowerSupply"`
	HasHistory               bool    `dbus:"HasHistory"`
	HasStatistics            bool    `dbus:"HasStatistics"`
	Online                   bool    `dbus:"Online"`
	Energy                   float64 `dbus:"Energy"`
	EnergyEmpty              float64 `dbus:"EnergyEmpty"`
	EnergyFull               float64 `dbus:"EnergyFull"`
	EnergyFullDesign         float64 `dbus:"EnergyFullDesign"`
	EnergyRate               float64 `dbus:"EnergyRate"`
	Voltage                  float64 `dbus:"Voltage"`
	ChargeCycles             int32   `dbus:"ChargeCycles"`
	Luminosity               float64 `dbus:"Luminosity"`
	TimeToEmpty              int64   `dbus:"TimeToEmpty"`
	TimeToFull               int64   `dbus:"TimeToFull"`
	Percentage               float64 `dbus:"Percentage"`
	Temperature              float64 `dbus:"Temperature"`
	IsPresent                bool    `dbus:"IsPresent"`
	State                    uint32  `dbus:"State"`
	IsRechargeable           bool    `dbus:"IsRechargeable"`
	Capacity                 float64 `dbus:"Capacity"`
	Technology               uint32  `dbus:"Technology"`
	WarningLevel             uint32  `dbus:"WarningLevel"`
	BatteryLevel             uint32  `dbus:"BatteryLevel"`
	IconName                 string  `dbus:"IconName"`
	ChargeStartThreshold     uint32  `dbus:"ChargeStartThreshold"`
	ChargeEndThreshold       uint32  `dbus:"ChargeEndThreshold"`
	ChargeThresholdEnabled   bool    `dbus:"ChargeThresholdEnabled"`
	ChargeThresholdSupported bool    `dbus:"ChargeThresholdSupported"`
	VoltageMinDesign         float64 `dbus:"VoltageMinDesign"`
	VoltageMaxDesign         float64 `dbus:"VoltageMaxDesign"`
	CapacityLevel            string  `dbus:"CapacityLevel"`
}

func ConnectUPower() (*dbus.Conn, <-chan *dbus.Signal, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		logging.Log.Warn().Msgf("battery: dbus: failed to connect to system bus")
		return nil, nil, err
	}

	device, err := GetDisplayDevice(conn)
	if err != nil {
		logging.Log.Warn().Msgf("battery: error getting display device props: %v", err)
		conn.Close()
		return nil, nil, err
	}

	if !IsValidSource(device) {
		logging.Log.Debug().Msgf("battery: no valid power source found")
		conn.Close()
		return nil, nil, fmt.Errorf("no valid power source found")
	}

	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)

	if err = conn.AddMatchSignal(
		dbus.WithMatchInterface(DBUS_PROPS_IFACE),
		dbus.WithMatchObjectPath(UP_DISPLAY_DEVICE_PATH),
	); err != nil {
		logging.Log.Warn().Msgf("battery: error matching signal: %v", err)
		conn.Close()
		return nil, nil, err
	}

	return conn, ch, nil
}

func IsValidSource(device UPowerDevice) bool {
	if device.Type != DeviceBattery && device.Type != DeviceUps {
		return false
	}
	return device.PowerSupply
}

func GetDisplayDevice(conn *dbus.Conn) (UPowerDevice, error) {
	var device UPowerDevice

	obj := conn.Object(UP_NAME, UP_DISPLAY_DEVICE_PATH)
	c := obj.Call(DBUS_PROPS_GETALL, 0, UP_IFACE)

	var props map[string]dbus.Variant
	err := c.Store(&props)
	if err != nil {
		logging.Log.Warn().Msgf("battery: error calling GetAll %v", err)
		return device, err
	}

	err = UnmarshalVardict(props, &device)
	if err != nil {
		logging.Log.Warn().Msgf("battery: unmarshal error: %v", err)
		return device, err
	}

	return device, nil
}

func UnmarshalVardict(vardict map[string]dbus.Variant, out interface{}) error {
	val := reflect.ValueOf(out)
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Struct {
		return errors.New("output must be a pointer to a struct")
	}

	structVal := val.Elem()
	structType := structVal.Type()

	for i := 0; i < structVal.NumField(); i++ {
		field := structType.Field(i)
		fieldVal := structVal.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		key := field.Name
		if tag, ok := field.Tag.Lookup("dbus"); ok {
			key = tag
		}

		variant, ok := vardict[key]
		if !ok {
			continue
		}

		variantVal := reflect.ValueOf(variant.Value())
		if !variantVal.Type().AssignableTo(field.Type) {
			return fmt.Errorf("cannot assign value of type %s to field %s (type %s)", variantVal.Type(), field.Name, field.Type)
		}

		fieldVal.Set(variantVal)
	}

	return nil
}

func HandleSignal(sig *dbus.Signal, device *UPowerDevice) {
	if sig.Path != UP_DISPLAY_DEVICE_PATH || sig.Name != DBUS_PROPS_SIGNAL {
		return
	}
	if len(sig.Body) != 3 {
		logging.Log.Warn().Msgf("battery: upower: invalid signal")
		return
	}
	vardict, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		logging.Log.Warn().Msgf("battery: upower: invalid signal")
		return
	}
	UnmarshalVardict(vardict, device)
	logging.Log.Debug().Msgf("battery: upower: signal: %v", sig.Name)
}
