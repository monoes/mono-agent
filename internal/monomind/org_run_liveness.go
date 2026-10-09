package monomind

import (
	"os"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// Pid reuse (monomind 2.24.3, monoes/monomind#573): a killed `org run`
// leaves runtime.json saying "running", and once the kernel hands its pid to
// an unrelated process a bare kill -0 keeps succeeding. monomind records the
// process's start identity as `pidStart` next to the pid; this mirrors its
// recordedPidLiveness (orgrt/run-liveness.ts) so a reused pid reads as dead.

// recordedPidAlive reports whether the process a record names by pid and,
// when it has one, pidStart is still running. A record without pidStart (or
// an identity that can't be read or compared) trusts the pid alone.
func recordedPidAlive(pid int, pidStart string) bool {
	if !daemonhb.ProcessAlive(pid) {
		return false
	}
	if pidStart == "" {
		return true
	}
	now := processStartID(pid)
	if now == "" {
		return true
	}
	return sameStartID(pidStart, now) != "different"
}

// processStartID is monomind's "linux:<boot id>:<start time>" identity of a
// process, or "" where /proc has none (other systems: the pid is trusted).
func processStartID(pid int) string {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(stat)
	// Fields after the parenthesised command name start at field 3 (state);
	// starttime is field 22. The name itself may contain spaces or ')'.
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(fields) < 20 {
		return ""
	}
	boot := ""
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		boot = strings.TrimSpace(string(b))
	}
	return "linux:" + boot + ":" + fields[22-3]
}

// sameStartID compares two identities: "same", "different", or "" when they
// were read by different methods and say nothing about the process. A
// missing boot id on either side compares the start time alone.
func sameStartID(recorded, now string) string {
	if !strings.HasPrefix(recorded, "linux:") || !strings.HasPrefix(now, "linux:") {
		if recorded == now {
			return "same"
		}
		if kindOf(recorded) != kindOf(now) {
			return ""
		}
		return "different"
	}
	rb, rs := splitLinuxID(recorded)
	nb, ns := splitLinuxID(now)
	if rb != "" && nb != "" && rb != nb {
		return "different"
	}
	if rs != ns {
		return "different"
	}
	return "same"
}

func kindOf(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[:i+1]
	}
	return id
}

func splitLinuxID(id string) (boot, start string) {
	rest := strings.TrimPrefix(id, "linux:")
	at := strings.LastIndexByte(rest, ':')
	if at < 0 {
		return "", rest
	}
	return rest[:at], rest[at+1:]
}
