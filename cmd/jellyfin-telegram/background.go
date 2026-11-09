package main

import "strings"

// schedulerParent is a Task Scheduler host. That process must stay the one the task
// watches, so a crash still restarts it. A console the user opened is not.
func schedulerParent(name string) bool {
	switch strings.ToLower(name) {
	case "taskhostw.exe", "taskeng.exe", "svchost.exe":
		return true
	default:
		return false
	}
}
