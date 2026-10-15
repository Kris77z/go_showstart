package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, errout io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errout, err); return 2 }
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "usage: bridge-order prepare|submit|status|recover [explicit flags]")
		return 0
	}
	if len(args) == 0 {
		return fail(fmt.Errorf("usage: bridge-order prepare|submit|status|recover [explicit flags]"))
	}
	command := args[0]
	if command != "prepare" && command != "submit" && command != "status" && command != "recover" {
		return fail(fmt.Errorf("unknown command %q", command))
	}
	fs := flag.NewFlagSet("bridge-order "+command, flag.ContinueOnError)
	fs.SetOutput(errout)
	var cfg config.Bridge
	var s client.BridgeSelection
	var key, performers string
	var confirmed, wait bool
	var timeout, interval time.Duration
	fs.StringVar(&cfg.Endpoint, "endpoint", "", "loopback bridge http endpoint")
	fs.StringVar(&cfg.AccountAlias, "account-alias", "", "bridge account alias")
	fs.StringVar(&cfg.Cusid, "cusid", "", "stable account cusid")
	fs.StringVar(&cfg.DeviceNo, "device-no", "", "bound device number")
	fs.StringVar(&key, "request-key", "", "stable operation key, not a query key")
	fs.DurationVar(&timeout, "timeout", 20*time.Second, "total operation deadline")
	fs.DurationVar(&interval, "poll-interval", 200*time.Millisecond, "serial recovery poll interval")
	fs.BoolVar(&wait, "wait", false, "recover only: poll with one total deadline")
	fs.BoolVar(&confirmed, "confirm", false, "submit only: explicitly create an unpaid order")
	fs.IntVar(&s.ActivityID, "activity-id", 0, "exact activity ID")
	fs.StringVar(&s.TicketID, "ticket-id", "", "exact ticket ID")
	fs.IntVar(&s.Quantity, "quantity", 0, "explicit quantity")
	fs.StringVar(&s.UnitPrice, "unit-price", "", "decimal unit price")
	fs.StringVar(&s.TotalAmount, "total-amount", "", "exact decimal total")
	fs.StringVar(&performers, "performer-ids", "", "explicit JSON string array; [] only if no real name required")
	fs.IntVar(&s.OrderType, "order-type", 0, "supported order type: 1")
	fs.StringVar(&s.Delivery, "delivery", "", "supported delivery: electronic")
	fs.StringVar(&s.CouponID, "coupon-id", "", "currently unsupported unless empty")
	fs.StringVar(&s.AddressID, "address-id", "", "currently unsupported unless empty")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return fail(fmt.Errorf("unexpected positional arguments"))
	}
	if timeout <= 0 || timeout > 60*time.Second || interval <= 0 {
		return fail(fmt.Errorf("timeout must be positive and <=60s; poll-interval must be positive"))
	}
	if wait && command != "recover" {
		return fail(fmt.Errorf("--wait is only supported by recover"))
	}
	if command == "submit" && !confirmed {
		return fail(fmt.Errorf("submit requires explicit --confirm"))
	}
	if command == "prepare" || command == "submit" {
		provided := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
		for _, name := range []string{"activity-id", "ticket-id", "quantity", "unit-price", "total-amount", "performer-ids", "order-type", "delivery"} {
			if !provided[name] {
				return fail(fmt.Errorf("--%s must be explicitly provided", name))
			}
		}
		if err := json.Unmarshal([]byte(performers), &s.PerformerIDs); err != nil {
			return fail(fmt.Errorf("performer-ids must be a JSON string array"))
		}
		if err := s.Validate(); err != nil {
			return fail(err)
		}
	}
	c, err := client.NewBridgeClient(cfg)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var result any
	switch command {
	case "prepare":
		result, err = c.Prepare(ctx, key, s)
	case "submit":
		result, err = c.Submit(ctx, key, s)
	case "status":
		result, err = c.Status(ctx, key, false)
	case "recover":
		if wait {
			result, err = c.Wait(ctx, key, interval)
		} else {
			result, err = c.Status(ctx, key, true)
		}
	}
	// Typed nil pointers inside an interface must not emit misleading JSON null.
	if raw, e := json.Marshal(result); e == nil && string(raw) != "null" {
		fmt.Fprintln(out, string(raw))
	}
	if err != nil {
		return fail(err)
	}
	if command == "submit" {
		if state, ok := result.(*client.BridgeOrderState); ok {
			switch state.State {
			case "rejected":
				return fail(fmt.Errorf("submit rejected: %s", state.Reason))
			case "started", "delivery_unknown":
				return fail(client.ErrDeliveryUnknown)
			}
		}
	}
	return 0
}
