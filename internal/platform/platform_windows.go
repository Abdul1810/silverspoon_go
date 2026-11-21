//go:build windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procGetClipboardData     = user32.NewProc("GetClipboardData")
	procRegisterClipboardFmt = user32.NewProc("RegisterClipboardFormatW")
	procIsClipboardFmtAvail  = user32.NewProc("IsClipboardFormatAvailable")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procGlobalLock           = kernel32.NewProc("GlobalLock")
	procGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	procGlobalSize           = kernel32.NewProc("GlobalSize")
	shell32                  = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
)

const cfUnicodeText = 13

func clipboardBytes(format uintptr) ([]byte, bool) {
	if ok, _, _ := procIsClipboardFmtAvail.Call(format); ok == 0 {
		return nil, false
	}
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	n, _, _ := procGlobalSize.Call(h)
	if n == 0 {
		return nil, true
	}
	b := make([]byte, n)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&b[0])), p, n)
	return b, true
}

// ReadClipboard returns the clipboard's HTML fragment (if any) and its
// plain text.
func ReadClipboard() (html, text string) {
	for i := 0; i < 5; i++ {
		if ok, _, _ := procOpenClipboard.Call(0); ok != 0 {
			break
		}
		if i == 4 {
			return "", ""
		}
	}
	defer procCloseClipboard.Call()
	name, _ := syscall.UTF16PtrFromString("HTML Format")
	if f, _, _ := procRegisterClipboardFmt.Call(uintptr(unsafe.Pointer(name))); f != 0 {
		if b, ok := clipboardBytes(f); ok {
			html = strings.TrimRight(string(b), "\x00")
		}
	}
	if b, ok := clipboardBytes(cfUnicodeText); ok && len(b) >= 2 {
		u := unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
		text = syscall.UTF16ToString(u)
	}
	return html, text
}

// Open opens a folder or URL with the shell.
func Open(target string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 {
		return errors.New("could not open " + target)
	}
	return nil
}

// MessageBox shows a native warning box (used before the window exists).
func MessageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x30)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func runPicker(script string) (string, bool) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(out))
	return p, p != ""
}

// PickFolder shows the system folder picker starting at start.
func PickFolder(title, start string) (string, bool) {
	return runPicker(`Add-Type -AssemblyName System.Windows.Forms;` +
		`$d = New-Object System.Windows.Forms.FolderBrowserDialog;` +
		`$d.Description = ` + psQuote(title) + `; $d.UseDescriptionForTitle = $true;` +
		`$d.SelectedPath = ` + psQuote(start) + `;` +
		`$f = New-Object System.Windows.Forms.Form -Property @{TopMost=$true};` +
		`if ($d.ShowDialog($f) -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }`)
}

// PickFile shows the system open-file picker for text files.
func PickFile(title string) (string, bool) {
	return runPicker(`Add-Type -AssemblyName System.Windows.Forms;` +
		`$d = New-Object System.Windows.Forms.OpenFileDialog;` +
		`$d.Title = ` + psQuote(title) + `; $d.Filter = 'Text Files (*.txt)|*.txt|All Files (*.*)|*.*';` +
		`$f = New-Object System.Windows.Forms.Form -Property @{TopMost=$true};` +
		`if ($d.ShowDialog($f) -eq 'OK') { [Console]::Out.Write($d.FileName) }`)
}

// Lock is the single-instance lock: the file is opened with no sharing and
// kept open, so a second copy (Go or the Python app's QLockFile) can't take it.
type Lock struct{ h syscall.Handle }

// ErrLocked means another instance holds the lock.
var ErrLocked = errors.New("already running")

// AcquireLock takes the lock at path.
func AcquireLock(path string) (*Lock, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, syscall.Errno(32)) { // ERROR_SHARING_VIOLATION {
			return nil, ErrLocked
		}
		return nil, err
	}
	var n uint32
	pid := []byte(itoa(os.Getpid()) + "\nSilverSpoon\n")
	syscall.SetEndOfFile(h)
	syscall.WriteFile(h, pid, &n, nil)
	return &Lock{h: h}, nil
}

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil {
		syscall.CloseHandle(l.h)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
