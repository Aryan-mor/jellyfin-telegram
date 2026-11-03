// Package fladder runs the Fladder web client and points it at this Jellyfin and Seerr.
package fladder

// Image is Fladder v0.11.1. The digest is pinned so a moved tag cannot change what runs.
const Image = "ghcr.io/donutware/fladder:v0.11.1@sha256:f93f1cfaf3ebd586820eb1c766a8e503dbe056efcac1dd3d15820b5a84fce458"

// ContainerName is stable so a copy left over from an earlier run can be removed.
const ContainerName = "jellyfin-telegram-fladder"

// Port is where Fladder listens on this machine.
const Port = 15080

// WebPath is the path on this program's port. Fladder is told the same path.
const WebPath = "/fladder"
