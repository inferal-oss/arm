package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

// reviewStoppedRestart runs before ordinary backend launch. The exact existing
// authority lock and elapsed review stay in this process through confirmation.
// Cancellation stops waiting and releases that lock; an arbitrary input Reader
// may remain blocked until its own read completes. No universal reader join is claimed.
func (a *app) reviewStoppedRestart(reason string, yes, dry bool) (handled bool, resultErr error) {
	review, needed, err := server.PrepareNodeRestart(a.ctx, a.home, reason)
	if err != nil {
		return true, err
	}
	if !needed {
		if dry {
			return true, a.print(map[string]any{"operation": "start-arm-authority", "home": a.home, "stopped_intent": false, "dry_run": true, "detail": "No stopped intent to clear. A normal explicit start would launch the backend."})
		}
		return false, nil
	}
	defer func() { resultErr = errors.Join(resultErr, review.Close()) }()
	plan, err := review.Plan()
	if err != nil {
		return true, err
	}
	if dry || !a.machine() || !yes {
		if err = a.print(plan); err != nil {
			return true, err
		}
	}
	if dry {
		return true, nil
	}
	if !yes {
		if a.machine() || !a.inputTTY {
			return true, model.Err("confirmation_required", "ARM is intentionally stopped; review arm start --reason PURPOSE --dry-run, then pass --yes to authorize this exact restart")
		}
		if _, err = fmt.Fprint(a.out, "\n"+a.paint("1;33", "Start a fresh ARM generation and retain earlier obligations? [y/N] ")); err != nil {
			return true, err
		}
		type reply struct {
			line string
			err  error
		}
		answers := make(chan reply, 1)
		go func() {
			line, e := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n')
			answers <- reply{line, e}
		}()
		select {
		case <-a.ctx.Done():
			return true, a.ctx.Err()
		case answer := <-answers:
			text := strings.ToLower(strings.TrimSpace(answer.line))
			if answer.err != nil || len(answer.line) > 256 || (text != "y" && text != "yes") {
				return true, model.Err("confirmation_declined", "Restart declined; stopped intent and earlier obligations were retained")
			}
		}
	}
	if err = review.Apply(a.ctx); err != nil {
		return true, err
	}
	// Release the exact lock before launching ordinary Serve. Running intent was
	// already authorized durably; launch errors must not claim a rollback/retry.
	if err = review.Close(); err != nil {
		return true, err
	}
	return false, nil
}
