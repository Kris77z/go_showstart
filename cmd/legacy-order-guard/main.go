package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/staparx/go_showstart/orderguard"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	precheck := len(args) > 0 && args[0] == "precheck"
	if precheck {
		args = args[1:]
	}
	fs := flag.NewFlagSet("legacy-order-guard", flag.ContinueOnError)
	var o orderguard.Options
	var timeout time.Duration
	fs.StringVar(&o.StateRoot, "state-root", "", "existing private state directory")
	fs.StringVar(&o.Cusid, "cusid", "", "account ID")
	fs.StringVar(&o.AccountAlias, "account-alias", "", "account alias")
	fs.StringVar(&o.DeviceNo, "device-no", "", "device number")
	fs.StringVar(&o.RequestKey, "request-key", "", "unique request key")
	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "lock and child deadline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	if precheck {
		if fs.NArg() != 0 {
			return fmt.Errorf("precheck accepts no child")
		}
		return orderguard.Precheck(ctx, o)
	}
	return orderguard.Run(ctx, o, fs.Args())
}
