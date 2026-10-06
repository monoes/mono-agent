package orgdesign

import (
	"encoding/json"
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
	switch {
	case s == "":
		d.Schedule = json.RawMessage("null")
	case scheduleRe.MatchString(s):
		if n, _ := strconv.Atoi(scheduleRe.FindStringSubmatch(s)[1]); n == 0 {
			return fmt.Errorf("schedule %q: the interval must be more than zero", s)
		}
		b, _ := json.Marshal(s)
		d.Schedule = b
	default:
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return fmt.Errorf("schedule %q: use an interval such as 30s, 15m or 2h", s)
		}
		d.Schedule = json.RawMessage(strconv.Itoa(n))
	}
	return nil
}
