package main

import "testing"

func TestSchedulerParentStaysTheService(t *testing.T) {
	for _, name := range []string{"taskhostw.exe", "TaskEng.exe", "svchost.exe"} {
		if !schedulerParent(name) {
			t.Errorf("%s should stay attached to the scheduled task", name)
		}
	}
	for _, name := range []string{"cmd.exe", "powershell.exe", "explorer.exe", ""} {
		if schedulerParent(name) {
			t.Errorf("%s is a window the user can close", name)
		}
	}
}
