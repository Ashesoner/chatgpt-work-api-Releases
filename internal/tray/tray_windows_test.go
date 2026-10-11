//go:build windows

package tray

import (
	"testing"
	"time"
	"unsafe"
)

func TestTrayNotificationEventUsesLowWordForVersion4(t *testing.T) {
	raw := uintptr((uint32(1) << 16) | wmRButtonUp)
	if got := trayNotificationEvent(raw); got != wmRButtonUp {
		t.Fatalf("event = %#x, want %#x", got, wmRButtonUp)
	}
}

func TestTrayRestoresIconAfterTaskbarCreated(t *testing.T) {
	implementation := newImplementation(nil, nil)
	tray, ok := implementation.(*windowsImplementation)
	if !ok {
		t.Fatal("unexpected tray implementation")
	}
	if err := tray.Start(); err != nil {
		t.Skipf("tray integration unavailable: %v", err)
	}
	defer tray.Close()

	tray.mu.Lock()
	hwnd := tray.hwnd
	taskbarCreated := tray.taskbarCreated
	tray.mu.Unlock()
	if hwnd == 0 || taskbarCreated == 0 {
		t.Fatal("tray window or TaskbarCreated message was not initialized")
	}

	nid := notifyIconData{CbSize: uint32(unsafe.Sizeof(notifyIconData{})), HWnd: hwnd, UID: 1}
	deleted, _, _ := procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if deleted == 0 {
		t.Fatal("failed to remove tray icon before recovery test")
	}
	posted, _, _ := procPostMessageW.Call(hwnd, uintptr(taskbarCreated), 0, 0)
	if posted == 0 {
		t.Fatal("failed to post TaskbarCreated message")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probe := notifyIconData{
			CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
			HWnd:   hwnd,
			UID:    1,
			UFlags: nifTip | nifShowTip,
		}
		setUTF16(probe.SzTip[:], "CWapi tray recovery test")
		modified, _, _ := procShellNotifyIconW.Call(0x00000001, uintptr(unsafe.Pointer(&probe)))
		if modified != 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("tray icon was not restored after TaskbarCreated")
}
