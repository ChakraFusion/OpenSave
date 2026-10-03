//go:build windows

package sessions

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// listProcesses walks the process table and asks each process for its full
// program path. A process whose path cannot be read is listed by its file name
// alone (Proc.Name): a game protected against tampering keeps its path from
// everyone, and matching by name within its install folder's programs is
// still exact enough.
func listProcesses() ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, err
	}
	var out []Proc
	buf := make([]uint16, windows.MAX_LONG_PATH)
	for {
		if pid := entry.ProcessID; pid > 4 {
			if exe := imagePath(pid, buf); exe != "" {
				out = append(out, Proc{PID: int(pid), Exe: exe})
			} else if name := windows.UTF16ToString(entry.ExeFile[:]); name != "" {
				// Its path is not ours to read: a game protected against
				// tampering, run elevated, or another user's. Known by name.
				out = append(out, Proc{PID: int(pid), Name: name})
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return out, nil
}

func imagePath(pid uint32, buf []uint16) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
