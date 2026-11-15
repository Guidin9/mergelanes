// Package platform holds what differs between operating systems outside the network code:
// privileges, single-instance locking, autostart and how errors reach a user with no console.
package platform

// StateDir is the state folder of the running instance (state.DefaultDir unless -state-dir says
// otherwise). main sets it before anything that writes there runs. On Windows it is the elevated
// process's own folder (see SecureDir): the start-at-sign-in task's XML and the weak host settings
// to restore are written there.
var StateDir string
