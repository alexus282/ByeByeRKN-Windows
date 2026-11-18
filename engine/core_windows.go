package engine

import (
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
)

// wintunCore is the real amneziawg-go device on a Wintun adapter. wintun.dll is loaded from the
// directory of the exe, so the build copies windows/wintun/amd64/wintun.dll next to it.
type wintunCore struct {
	logf func(format string, args ...any)
}

func (c wintunCore) Open(name string, mtu int, uapi string) (Session, error) {
	tunDevice, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, err
	}
	logger := &device.Logger{Verbosef: device.DiscardLogf, Errorf: func(format string, args ...any) {
		if c.logf != nil {
			c.logf("awg: "+format, args...)
		}
	}}
	dev := device.NewDevice(tunDevice, conn.NewDefaultBind(), logger)
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	return &wintunSession{dev: dev}, nil
}

type wintunSession struct {
	dev *device.Device
}

func (s *wintunSession) Stats() (Stats, error) {
	text, err := s.dev.IpcGet()
	if err != nil {
		return Stats{}, err
	}
	return ParseIpcGet(text), nil
}

func (s *wintunSession) Close() { s.dev.Close() }
