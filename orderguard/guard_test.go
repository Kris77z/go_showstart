package orderguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func options(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return Options{StateRoot: root, Cusid: "account-测试", AccountAlias: "test", DeviceNo: "device", RequestKey: "request-1"}
}

func TestDirectorySyncFailurePreventsLegacyStartAndRetryResyncs(t *testing.T) {
	o := options(t)
	marker := filepath.Join(o.StateRoot, "must-not-start")
	original := syncDirectory
	defer func() { syncDirectory = original }()
	var synced []string
	parent := filepath.Dir(o.StateRoot)
	syncDirectory = func(path string) error {
		synced = append(synced, path)
		if path == parent {
			return errors.New("simulated parent fsync failure")
		}
		return original(path)
	}
	if err := Run(context.Background(), o, helperArgs(marker, false)); err == nil {
		t.Fatal("parent fsync failure allowed legacy start")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy child ran: %v", err)
	}
	if len(synced) != 2 || synced[0] != o.StateRoot || synced[1] != parent {
		t.Fatalf("wrong directory sync order: %v", synced)
	}
	synced = nil
	syncDirectory = func(path string) error { synced = append(synced, path); return original(path) }
	if err := Precheck(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 2 || synced[0] != o.StateRoot || synced[1] != parent {
		t.Fatalf("retry skipped directory sync: %v", synced)
	}
}

func TestInvalidParentCannotStartLegacy(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "writable"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			marker := filepath.Join(o.StateRoot, "must-not-start")
			switch kind {
			case "missing":
				o.StateRoot = filepath.Join(o.StateRoot, "missing", "state")
			case "symlink":
				link := filepath.Join(o.StateRoot, "link")
				if err := os.Symlink(o.StateRoot, link); err != nil {
					t.Fatal(err)
				}
				o.StateRoot = filepath.Join(link, "state")
				if err := os.Mkdir(o.StateRoot, 0700); err != nil {
					t.Fatal(err)
				}
			case "writable":
				parent := o.StateRoot
				o.StateRoot = filepath.Join(parent, "state")
				if err := os.Mkdir(o.StateRoot, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(parent, 0770); err != nil {
					t.Fatal(err)
				}
			}
			if err := Run(context.Background(), o, helperArgs(marker, false)); err == nil {
				t.Fatal("invalid parent allowed legacy start")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("legacy child ran: %v", err)
			}
		})
	}
}
func helperArgs(marker string, hold bool) []string {
	mode := "once"
	if hold {
		mode = "hold"
	}
	return []string{os.Args[0], "-test.run=^TestHelperProcess$", "--", "orderguard-helper", marker, mode}
}
func TestHelperProcess(t *testing.T) {
	var pos int
	for i, a := range os.Args {
		if a == "orderguard-helper" {
			pos = i
			break
		}
	}
	if pos == 0 {
		return
	}
	marker := os.Args[pos+1]
	mode := os.Args[pos+2]
	if mode == "grandchild" {
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(2)
		}
		for {
			if _, err = f.Write([]byte("tick\n")); err != nil {
				os.Exit(2)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if mode == "spawn-hold" || mode == "spawn-exit" {
		child := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "--", "orderguard-helper", marker+".grandchild", "grandchild")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if info, err := os.Stat(marker + ".grandchild"); err == nil && info.Size() > 0 {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(3)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(marker, []byte("executed"), 0600); err != nil {
		os.Exit(2)
	}
	if mode == "hold" || mode == "spawn-hold" {
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(0)
}
func journal(t *testing.T, o Options) Journal {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var j Journal
	if err = json.Unmarshal(data, &j); err != nil {
		t.Fatal(err)
	}
	return j
}
func TestExitZeroStillUnknownAndBlocks(t *testing.T) {
	o := options(t)
	marker := filepath.Join(t.TempDir(), "marker")
	args := append(helperArgs(marker, false), "secret-not-for-journal")
	err := Run(context.Background(), o, args)
	if !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	j := journal(t, o)
	r := j.Records[o.RequestKey]
	if r.State != "delivery_unknown" || r.Backend != "legacy" {
		t.Fatalf("unexpected record %+v", r)
	}
	if r.StartedAtUnixMS == nil || *r.StartedAtUnixMS <= 0 {
		t.Fatal("missing legacy start time")
	}
	jp := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json")
	data, err := os.ReadFile(jp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-not-for-journal") || strings.Contains(string(data), marker) {
		t.Fatal("child arguments leaked into journal")
	}
	st, err := os.Stat(jp)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("journal is not private", err)
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	o.RequestKey = "request-2"
	if err = Run(context.Background(), o, helperArgs(marker, false)); err == nil {
		t.Fatal("unresolved account accepted")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("second child executed: %v", err)
	}
}
func TestUnsafeStateNeverExecutes(t *testing.T) {
	cases := []string{"corrupt", "duplicate", "schema", "permission", "symlink", "unknown", "started", "queued", "rejected", "record-version", "root-mode", "missing-root", "root-file", "lock-symlink", "lock-mode"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			o := options(t)
			p := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json")
			marker := filepath.Join(t.TempDir(), "marker")
			switch name {
			case "corrupt":
				os.WriteFile(p, []byte("{"), 0600)
			case "duplicate":
				os.WriteFile(p, []byte(`{"version":1,"cusid":"account-测试","records":{"unresolved":{}},"records":{}}`), 0600)
			case "schema":
				os.WriteFile(p, []byte(`{"version":2,"cusid":"account-测试","records":{}}`), 0600)
			case "permission":
				os.WriteFile(p, []byte("{}"), 0644)
				os.Chmod(p, 0644)
			case "symlink":
				os.Symlink(filepath.Join(t.TempDir(), "absent"), p)
			case "root-mode":
				os.Chmod(o.StateRoot, 0755)
			case "missing-root":
				o.StateRoot = filepath.Join(o.StateRoot, "absent")
			case "root-file":
				o.StateRoot = filepath.Join(o.StateRoot, "file")
				os.WriteFile(o.StateRoot, nil, 0700)
			case "lock-symlink":
				os.Symlink(filepath.Join(t.TempDir(), "absent"), filepath.Join(o.StateRoot, BaseName(o.Cusid)+".lock"))
			case "lock-mode":
				lp := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".lock")
				os.WriteFile(lp, nil, 0644)
				os.Chmod(lp, 0644)
			default:
				state := name
				if name == "unknown" {
					state = "delivery_unknown"
				}
				r := validAppRecord(t, o)
				r.RequestKey = "old-request"
				r.State = state
				if name == "record-version" {
					r.Version = 2
					r.State = "created"
					r.OrderID = "order-123"
				}
				j := Journal{Version: 1, Cusid: o.Cusid, Records: map[string]Record{"old-request": r}}
				data, _ := json.Marshal(j)
				os.WriteFile(p, data, 0600)
			}
			if err := Run(context.Background(), o, helperArgs(marker, false)); err == nil {
				t.Fatal("unsafe state accepted")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("child executed: %v", err)
			}
		})
	}
}

func TestCanonicalSchemaFieldsCannotHideUnresolvedState(t *testing.T) {
	for _, name := range []string{"state-conflict", "state-uppercase", "binding-uppercase", "root-uppercase"} {
		t.Run(name, func(t *testing.T) {
			o := options(t)
			record := validAppRecord(t, o)
			j := Journal{Version: 1, Cusid: o.Cusid, Records: map[string]Record{record.RequestKey: record}}
			raw, _ := json.Marshal(j)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			r := fields["records"].(map[string]any)[record.RequestKey].(map[string]any)
			switch name {
			case "state-conflict":
				r["state"] = "delivery_unknown"
				r["State"] = "created"
			case "state-uppercase":
				delete(r, "state")
				r["State"] = "created"
			case "binding-uppercase":
				b := r["binding"].(map[string]any)
				b["CUSID"] = b["cusid"]
				delete(b, "cusid")
			case "root-uppercase":
				fields["Version"] = fields["version"]
				delete(fields, "version")
			}
			raw, _ = json.Marshal(fields)
			if err := os.WriteFile(filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "marker")
			if err := Run(context.Background(), o, helperArgs(marker, false)); err == nil {
				t.Fatal("noncanonical schema accepted")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("child executed despite schema conflict", err)
			}
		})
	}
}
func TestLockHeldThroughChildAndStable(t *testing.T) {
	o := options(t)
	marker := filepath.Join(t.TempDir(), "marker")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o, helperArgs(marker, true)) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lockPath := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".lock")
	initial := journal(t, o).Records[o.RequestKey]
	if initial.State != "started" {
		t.Fatal("child started without durable claim")
	}
	if initial.StartedAtUnixMS == nil || *initial.StartedAtUnixMS <= 0 {
		t.Fatal("missing initial start time")
	}
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	blocked, stop := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	if err = Precheck(blocked, o); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock not held: %v", err)
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, ErrDeliveryUnknown) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not stop")
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock inode replaced")
	}
	if journal(t, o).Records[o.RequestKey].State != "delivery_unknown" {
		t.Fatal("missing unknown")
	}
	final := journal(t, o).Records[o.RequestKey]
	if final.StartedAtUnixMS == nil || *final.StartedAtUnixMS != *initial.StartedAtUnixMS {
		t.Fatal("start time changed during unknown transition")
	}
}

func TestDescendantStopsBeforeUnlock(t *testing.T) {
	for _, mode := range []string{"spawn-hold", "spawn-exit"} {
		t.Run(mode, func(t *testing.T) {
			o := options(t)
			marker := filepath.Join(t.TempDir(), "ready")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args := helperArgs(marker, false)
			args[len(args)-1] = mode
			done := make(chan error, 1)
			go func() { done <- Run(ctx, o, args) }()
			deadline := time.Now().Add(8 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("launcher not ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "spawn-hold" {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrDeliveryUnknown) {
					t.Fatal(err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("launcher did not stop")
			}
			// Wait for any write already in the kernel at SIGKILL delivery to finish.
			time.Sleep(50 * time.Millisecond)
			before, err := os.Stat(marker + ".grandchild")
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			after, err := os.Stat(marker + ".grandchild")
			if err != nil {
				t.Fatal(err)
			}
			if before.Size() != after.Size() {
				t.Fatal("grandchild kept writing after wrapper returned")
			}
			if journal(t, o).Records[o.RequestKey].State != "delivery_unknown" {
				t.Fatal("missing unknown state")
			}
		})
	}
}
func TestConcurrentClaimsStartOnlyOneChild(t *testing.T) {
	o := options(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Run(context.Background(), o, helperArgs(filepath.Join(o.StateRoot, "marker"), false))
		}()
	}
	wg.Wait()
	close(errs)
	unknown := 0
	for err := range errs {
		if errors.Is(err, ErrDeliveryUnknown) {
			unknown++
		} else if err == nil {
			t.Fatal("unexpected success")
		}
	}
	if unknown != 1 {
		t.Fatalf("children completed %d times", unknown)
	}
}

func TestSharedSchemaAndIdentifierBoundaries(t *testing.T) {
	o := options(t)
	o.AccountAlias = "Alias 中文 / spaces"
	o.DeviceNo = "device:with.period"
	if err := validateOptions(o); err != nil {
		t.Fatalf("binding incorrectly constrained: %v", err)
	}
	for _, key := range []string{"short", "request.bad", strings.Repeat("a", 129), "request-1\n"} {
		o.RequestKey = key
		if err := validateOptions(o); err == nil {
			t.Fatalf("accepted key %q", key)
		}
	}
	o.RequestKey = "request-1"
	started := int64(1)
	r := validAppRecord(t, o)
	r.StartedAtUnixMS = &started
	if err := validateRecord(r.RequestKey, r, o.Cusid); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"order_id", "order_job_key", "core_order_key", "selection", "started_at_unix_ms", "reason"} {
		data, _ := json.Marshal(r)
		var raw map[string]any
		json.Unmarshal(data, &raw)
		raw[field] = nil
		bad, _ := json.Marshal(raw)
		var decoded Record
		if err := json.Unmarshal(bad, &decoded); err == nil {
			t.Fatalf("accepted null %s", field)
		}
	}
	for _, id := range []string{"bad.id", "has space", strings.Repeat("a", 129)} {
		for index := 0; index < 3; index++ {
			bad := r
			switch index {
			case 0:
				bad.OrderID = id
			case 1:
				bad.OrderJobKey = id
			case 2:
				bad.CoreOrderKey = id
			}
			if err := validateRecord(bad.RequestKey, bad, o.Cusid); err == nil {
				t.Fatalf("accepted ID %q in field %d", id, index)
			}
		}
	}
	for _, selection := range []string{`null`, `[]`, `"object"`, `1`} {
		bad := r
		bad.Selection = json.RawMessage(selection)
		if err := validateRecord(bad.RequestKey, bad, o.Cusid); err == nil {
			t.Fatalf("accepted selection %s", selection)
		}
	}
	started = 0
	if err := validateRecord(r.RequestKey, r, o.Cusid); err == nil {
		t.Fatal("accepted zero started time")
	}
}

func validAppRecord(t *testing.T, o Options) Record {
	t.Helper()
	selection := json.RawMessage(`{"activity_id":123,"ticket_id":"ticket-1","quantity":1,"unit_price":"10.00","total_amount":"10.00","performer_ids":[],"order_type":1,"delivery":"electronic","coupon_id":"","address_id":""}`)
	digest, err := appSelectionDigest(selection)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "a8185046f651bbcc7c660371152deac504fa41bbc2715ad89984777869a9b164" {
		t.Fatal("digest differs from Python canonical JSON", digest)
	}
	started := int64(1)
	return Record{Version: 1, RequestKey: "app-record-1", Backend: "app", Endpoint: "/nj/order/order", Binding: Binding{AccountAlias: o.AccountAlias, Cusid: o.Cusid, DeviceNo: o.DeviceNo}, State: "created", SelectionDigest: digest, Selection: selection, StartedAtUnixMS: &started, UpdatedAtUnixMS: 2, OrderID: "ORDER_123"}
}

func TestAppRecordsFailClosedBeforeChild(t *testing.T) {
	for _, mode := range []string{"valid", "digest", "selection-missing", "selection-field-missing", "selection-extra-field", "selection-null-field", "selection-float", "started-missing", "started-zero", "time-reversed", "legacy-started-missing"} {
		t.Run(mode, func(t *testing.T) {
			o := options(t)
			r := validAppRecord(t, o)
			marker := filepath.Join(t.TempDir(), "marker")
			switch mode {
			case "digest":
				r.SelectionDigest = strings.Repeat("a", 64)
			case "selection-missing":
				r.Selection = nil
			case "selection-field-missing":
				var m map[string]any
				json.Unmarshal(r.Selection, &m)
				delete(m, "coupon_id")
				r.Selection, _ = json.Marshal(m)
			case "selection-extra-field":
				var m map[string]any
				json.Unmarshal(r.Selection, &m)
				m["extra"] = true
				r.Selection, _ = json.Marshal(m)
			case "selection-null-field":
				r.Selection = bytes.Replace(r.Selection, []byte(`"coupon_id":""`), []byte(`"coupon_id":null`), 1)
			case "selection-float":
				r.Selection = bytes.Replace(r.Selection, []byte(`"quantity":1`), []byte(`"quantity":1.0`), 1)
			case "started-missing":
				r.StartedAtUnixMS = nil
			case "started-zero":
				v := int64(0)
				r.StartedAtUnixMS = &v
			case "time-reversed":
				v := int64(3)
				r.StartedAtUnixMS = &v
			case "legacy-started-missing":
				r.Backend = "legacy"
				r.Selection = nil
				r.StartedAtUnixMS = nil
			}
			j := Journal{Version: 1, Cusid: o.Cusid, Records: map[string]Record{r.RequestKey: r}}
			if err := write(o, &j); err != nil {
				t.Fatal(err)
			}
			err := Run(context.Background(), o, helperArgs(marker, false))
			if mode == "valid" {
				if !errors.Is(err, ErrDeliveryUnknown) {
					t.Fatalf("valid app record blocks rollback: %v", err)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid app record accepted")
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("child executed: %v", err)
				}
			}
		})
	}
}
