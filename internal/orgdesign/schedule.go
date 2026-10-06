package orgdesign

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var scheduleRe = regexp.MustCompile(`^(\d+)\s*(s|m|h)$`)

// SetSchedule sets the org's run interval in monomind's format ("45s",
// "15m", "2h"; bare digits are minutes, written as a number); "" clears it.
// Any org may be scheduled, a sections org included (monomind 2.24+): every
// tick starts a fresh run with its own document store and nothing carries
// forward from the tick before.
func (d *Doc) SetSchedule(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		d.Schedule = json.RawMessage("null")
		return nil
	}
	unit := "m"
	digits := s
	if m := scheduleRe.FindStringSubmatch(s); m != nil {
		digits, unit = m[1], m[2]
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			return fmt.Errorf("schedule %q: the interval is too long (the most monomind's timer takes is about 24 days)", s)
		}
		return fmt.Errorf("schedule %q: use an interval such as 30s, 15m or 2h", s)
	}
	if n == 0 {
		return fmt.Errorf("schedule %q: the interval must be more than zero", s)
	}
	if n > maxScheduleMs/unitMs[unit] {
		return fmt.Errorf("schedule %q: the interval is too long (the most monomind's timer takes is about 24 days)", s)
	}
	if unit == "m" && digits == s {
		d.Schedule = json.RawMessage(strconv.FormatUint(n, 10))
		return nil
	}
	b, _ := json.Marshal(s)
	d.Schedule = b
	return nil
}

// maxScheduleMs is Node's setInterval limit (2^31-1 ms): monomind's scheduler
// passes the interval straight to it, and a longer one fires every millisecond.
const maxScheduleMs = 1<<31 - 1

var unitMs = map[string]uint64{"s": 1000, "m": 60_000, "h": 3_600_000}
