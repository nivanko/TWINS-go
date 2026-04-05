//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32                                      = syscall.NewLazyDLL("user32.dll")
	kernel32                                    = syscall.NewLazyDLL("kernel32.dll")
	shell32                                     = syscall.NewLazyDLL("shell32.dll")
	procSendMessageW                            = user32.NewProc("SendMessageW")
	procLoadImageW                              = user32.NewProc("LoadImageW")
	procEnumWindows                             = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId                = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible                         = user32.NewProc("IsWindowVisible")
	procGetWindow                               = user32.NewProc("GetWindow")
	procSetClassLongPtrW                        = user32.NewProc("SetClassLongPtrW")
	procSetWindowPos                            = user32.NewProc("SetWindowPos")
	procGetModuleHandleW                        = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcessId                     = kernel32.NewProc("GetCurrentProcessId")
	procSetCurrentProcessExplicitAppUserModelID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procSHGetPropertyStoreForWindow             = shell32.NewProc("SHGetPropertyStoreForWindow")
)

const (
	wmSetIcon           = 0x0080
	iconBig             = 1
	iconSmall           = 0
	imageIcon           = 1
	lrDefaultSize       = 0x00000040
	lrShared            = 0x00008000
	gwOwner             = 4
	swpNoSize           = 0x0001
	swpNoMove           = 0x0002
	swpNoZOrder         = 0x0004
	swpNoActivate       = 0x0010
	swpFrameChanged     = 0x0020
	vtLPWSTR            = 31
	wailsIconResID      = 3 // Wails AppIconID (winc/app.go:19)
	windowsShellIconRes = 1
)

var (
	gclpHIcon   int32 = -14
	gclpHIconSm int32 = -34

	twinsAppUserModelID = "com.twins.wallet"
	twinsDisplayName    = "TWINS Wallet"
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type propertyKey struct {
	Fmtid guid
	Pid   uint32
}

type propVariant struct {
	VT         uint16
	WReserved1 uint16
	WReserved2 uint16
	WReserved3 uint16
	Val        uintptr
	Padding    uintptr
}

type iPropertyStore struct {
	lpVtbl *iPropertyStoreVtbl
}

type iPropertyStoreVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetCount       uintptr
	GetAt          uintptr
	GetValue       uintptr
	SetValue       uintptr
	Commit         uintptr
}

var (
	iidIPropertyStore = guid{Data1: 0x886D8EEB, Data2: 0x8CF2, Data3: 0x4446, Data4: [8]byte{0x8D, 0x02, 0xCD, 0xBA, 0x1D, 0xBD, 0xCF, 0x99}}

	pkeyAppUserModelID                   = propertyKey{Fmtid: guid{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}, Pid: 5}
	pkeyAppUserModelRelaunchCommand      = propertyKey{Fmtid: guid{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}, Pid: 2}
	pkeyAppUserModelRelaunchIconResource = propertyKey{Fmtid: guid{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}, Pid: 3}
	pkeyAppUserModelRelaunchDisplayName  = propertyKey{Fmtid: guid{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}, Pid: 4}
)

// dispatchSystrayToMainThread calls startFn directly on Windows.
func dispatchSystrayToMainThread(startFn func()) {
	startFn()
}

// hideDockIcon is a no-op on Windows.
func hideDockIcon() {}

// showDockIcon is a no-op on Windows.
func showDockIcon() {}

func configureWindowsTaskbarIdentity() {
	appID, err := syscall.UTF16PtrFromString(twinsAppUserModelID)
	if err != nil {
		fmt.Printf("configureWindowsTaskbarIdentity: invalid AppUserModelID: %v\n", err)
		return
	}

	hr, _, _ := procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(appID)))
	if hr != 0 {
		fmt.Printf("configureWindowsTaskbarIdentity: SetCurrentProcessExplicitAppUserModelID failed: 0x%x\n", hr)
		return
	}

	fmt.Println("configureWindowsTaskbarIdentity: process AppUserModelID set")
}

// shouldStartTray returns false on Windows — the system tray icon
// is not used on Windows. The tray manager is still created but
// never started, so all tray-dependent code paths (minimize-to-tray,
// hide tray icon setting) gracefully no-op via the started guard.
func shouldStartTray() bool { return false }

// splashHeightExtra returns extra pixels to add to splash window height
// to compensate for the title bar on Windows. Wails v2 on Windows uses
// outer window dimensions (including title bar), so the content area
// is smaller than the requested height.
func splashHeightExtra() int { return 38 }

// setTaskbarIcon finds the application window and sets ICON_BIG from the
// .exe resource so the Windows taskbar shows the TWINS icon.
// Wails v2 only sets ICON_SMALL (title bar); this fills in ICON_BIG.
// Called from domReady after the window is created.
func setTaskbarIcon() {
	hModule, _, _ := procGetModuleHandleW.Call(0)
	if hModule == 0 {
		fmt.Println("setTaskbarIcon: GetModuleHandle failed")
		return
	}

	// Load taskbar-sized icon from the embedded .exe resources.
	// Some Windows shells are picky about which resource group they use,
	// so try both the Wails-specific ID and the standard shell icon ID.
	hIcon, iconID := loadEmbeddedIcon(hModule, 32, 32)
	if hIcon == 0 {
		fmt.Println("setTaskbarIcon: LoadImage failed for both icon IDs")
		return
	}

	hIconSmall, _ := loadEmbeddedIcon(hModule, 16, 16)
	if hIconSmall == 0 {
		hIconSmall = hIcon
	}

	windows := findOwnWindows()
	if len(windows) == 0 {
		fmt.Println("setTaskbarIcon: could not find application window")
		return
	}

	for _, hwnd := range windows {
		applyWindowIcon(hwnd, hIcon, hIconSmall)
		applyTaskbarProperties(hwnd)
	}

	fmt.Printf("setTaskbarIcon: applied icon resource %d to %d window(s)\n", iconID, len(windows))
}

func applyTaskbarProperties(hwnd uintptr) {
	store, err := getWindowPropertyStore(hwnd)
	if err != nil {
		fmt.Printf("setTaskbarIcon: SHGetPropertyStoreForWindow failed: %v\n", err)
		return
	}
	defer store.Release()

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("setTaskbarIcon: could not resolve executable path: %v\n", err)
		return
	}

	relaunchCommand := fmt.Sprintf("\"%s\"", exePath)
	relaunchIcon := fmt.Sprintf("%s,-1", exePath)

	if err := store.SetString(pkeyAppUserModelRelaunchCommand, relaunchCommand); err != nil {
		fmt.Printf("setTaskbarIcon: Set RelaunchCommand failed: %v\n", err)
		return
	}
	if err := store.SetString(pkeyAppUserModelRelaunchDisplayName, twinsDisplayName); err != nil {
		fmt.Printf("setTaskbarIcon: Set RelaunchDisplayName failed: %v\n", err)
		return
	}
	if err := store.SetString(pkeyAppUserModelRelaunchIconResource, relaunchIcon); err != nil {
		fmt.Printf("setTaskbarIcon: Set RelaunchIconResource failed: %v\n", err)
		return
	}
	if err := store.SetString(pkeyAppUserModelID, twinsAppUserModelID); err != nil {
		fmt.Printf("setTaskbarIcon: Set AppUserModelID failed: %v\n", err)
		return
	}
	if err := store.Commit(); err != nil {
		fmt.Printf("setTaskbarIcon: Commit taskbar properties failed: %v\n", err)
		return
	}
}

func loadEmbeddedIcon(hModule uintptr, width, height uintptr) (uintptr, int) {
	for _, iconID := range []int{wailsIconResID, windowsShellIconRes} {
		hIcon, _, _ := procLoadImageW.Call(
			hModule,
			uintptr(iconID),
			imageIcon,
			width,
			height,
			lrDefaultSize|lrShared,
		)
		if hIcon != 0 {
			return hIcon, iconID
		}
	}

	return 0, 0
}

func applyWindowIcon(hwnd, hIcon, hIconSmall uintptr) {
	procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIcon)
	procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIconSmall)

	// Update the window class icons as well. Some Windows taskbar paths
	// consult the class icon instead of only the WM_SETICON state.
	procSetClassLongPtrW.Call(hwnd, uintptr(gclpHIcon), hIcon)
	procSetClassLongPtrW.Call(hwnd, uintptr(gclpHIconSm), hIconSmall)

	// Force a non-destructive frame refresh so the taskbar button picks up
	// the updated icon without recreating the window.
	procSetWindowPos.Call(
		hwnd,
		0,
		0,
		0,
		0,
		0,
		swpNoSize|swpNoMove|swpNoZOrder|swpNoActivate|swpFrameChanged,
	)
}

// findOwnWindows enumerates visible top-level windows owned by this process.
// Applying the icon to all of them is more reliable than assuming the first
// window we see is the one backing the taskbar button.
func findOwnWindows() []uintptr {
	pid, _, _ := procGetCurrentProcessId.Call()
	windows := make([]uintptr, 0, 2)

	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var windowPid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&windowPid)))
		if uintptr(windowPid) != pid {
			return 1
		}

		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}

		owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
		if owner != 0 {
			return 1
		}

		windows = append(windows, hwnd)
		return 1
	})

	procEnumWindows.Call(cb, 0)
	return windows
}

func getWindowPropertyStore(hwnd uintptr) (*iPropertyStore, error) {
	var store *iPropertyStore
	hr, _, _ := procSHGetPropertyStoreForWindow.Call(
		hwnd,
		uintptr(unsafe.Pointer(&iidIPropertyStore)),
		uintptr(unsafe.Pointer(&store)),
	)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("HRESULT 0x%x", uint32(hr))
	}
	if store == nil {
		return nil, fmt.Errorf("nil property store")
	}
	return store, nil
}

func (p *iPropertyStore) Release() {
	if p == nil || p.lpVtbl == nil {
		return
	}
	syscall.SyscallN(p.lpVtbl.Release, uintptr(unsafe.Pointer(p)))
}

func (p *iPropertyStore) SetString(key propertyKey, value string) error {
	ptr, utf16, err := utf16Ptr(value)
	if err != nil {
		return err
	}

	var pv propVariant
	pv.VT = vtLPWSTR
	pv.Val = uintptr(unsafe.Pointer(ptr))

	hr, _, _ := syscall.SyscallN(
		p.lpVtbl.SetValue,
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&pv)),
	)
	runtime.KeepAlive(utf16)
	if int32(hr) < 0 {
		return fmt.Errorf("HRESULT 0x%x", uint32(hr))
	}
	return nil
}

func (p *iPropertyStore) Commit() error {
	hr, _, _ := syscall.SyscallN(
		p.lpVtbl.Commit,
		uintptr(unsafe.Pointer(p)),
	)
	if int32(hr) < 0 {
		return fmt.Errorf("HRESULT 0x%x", uint32(hr))
	}
	return nil
}

func utf16Ptr(value string) (*uint16, []uint16, error) {
	utf16, err := syscall.UTF16FromString(value)
	if err != nil {
		return nil, nil, err
	}
	return &utf16[0], utf16, nil
}
