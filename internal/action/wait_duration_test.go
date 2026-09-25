package action

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestStepWaitZeroDurationDoesNotWait(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	for _, d := range []interface{}{0, 0.0, "0"} {
		start := time.Now()
		res, err := ae.stepWait(context.Background(), StepDef{ID: "w", Type: "wait", Duration: d})
		if err != nil || !res.Success {
			t.Fatalf("duration %v: %v %+v", d, err, res)
		}
		if el := time.Since(start); el > time.Second {
			t.Fatalf("duration %v waited %s, want no wait", d, el)
		}
	}
}
