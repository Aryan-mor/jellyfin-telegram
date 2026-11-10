//go:build windows

package setup

import "golang.org/x/sys/windows"

// browseTop lists the drives.
func browseTop() (Listing, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Top: true, Dirs: []string{}}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) != 0 {
			out.Dirs = append(out.Dirs, string(rune('A'+i))+`:\`)
		}
	}
	return out, nil
}
