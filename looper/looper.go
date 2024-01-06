package looper

import (
	"time"

	"github.com/gprossliner/xhdl"
	"k8s.io/klog/v2"
)

// Loop enters a retry-loop with cancelation support
// return true from the fn function to exit the loop
func Loop(ctx xhdl.Context, sleep time.Duration, fn func(ctx xhdl.Context) (exit bool)) {

	for {
		exit := fn(ctx)
		if exit {
			return
		}

		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			klog.Info("Context has been cancled")
			return

		case <-timer.C:
			continue
		}
	}
}
