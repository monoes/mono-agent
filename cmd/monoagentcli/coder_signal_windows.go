//go:build windows

package main

import "errors"

// monomind reports no background processes on Windows (v1), and a pid has
// no recorded identity there, so nothing reaches these.
var errNoSignals = errors.New("stopping background processes is not supported on Windows")

func processAlive(int) bool { return false }

func terminateProcess(int) error { return errNoSignals }

func killProcess(int) error { return errNoSignals }
