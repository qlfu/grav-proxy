package tray

import (
	"fmt"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCrossThreadPostCloseExitsMessageLoop(t *testing.T) {
	hwndCh := make(chan uintptr, 1)
	doneCh := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		clsName, _ := windows.UTF16PtrFromString(fmt.Sprintf("GravProxyTrayTest_%d", time.Now().UnixNano()))
		wndProc := syscall.NewCallback(func(hwnd uintptr, umsg uint32, wParam, lParam uintptr) uintptr {
			switch umsg {
			case wmClose:
				_, _, _ = procDestroyWindow.Call(hwnd)
				return 0
			case wmDestroy:
				_, _, _ = procPostQuitMessage.Call(0)
				return 0
			}
			ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(umsg), wParam, lParam)
			return ret
		})

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   wndProc,
			hInstance:     hInst,
			lpszClassName: clsName,
		}
		_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		hwnd, _, err := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(clsName)),
			uintptr(unsafe.Pointer(clsName)),
			0, 0, 0, 0, 0, 0, 0, hInst, 0,
		)
		if hwnd == 0 {
			doneCh <- fmt.Errorf("CreateWindowExW failed: %v", err)
			return
		}
		hwndCh <- hwnd

		var m msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		doneCh <- nil
	}()

	var hwnd uintptr
	select {
	case hwnd = <-hwndCh:
	case err := <-doneCh:
		t.Fatalf("message loop exited before window ready: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for test window creation")
	}

	ok, _, postErr := procPostMessageW.Call(hwnd, wmClose, 0, 0)
	if ok == 0 {
		t.Fatalf("PostMessageW(wmClose) failed: %v", postErr)
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("message loop failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("cross-thread PostMessageW(wmClose) did not terminate message loop within 1s")
	}
}
