// Package orderguard serializes legacy processes with the bridge account journal.
// It targets Linux and macOS (flock and O_NOFOLLOW); Windows is unsupported.
// Descendants that deliberately escape their process group (for example with
// setsid) require separate operator control.
package orderguard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/staparx/go_showstart/client"
)

var ErrDeliveryUnknown = errors.New("legacy delivery_unknown: inspect the account before another submission")

var requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Binding struct {
	AccountAlias string `json:"account_alias"`
	Cusid        string `json:"cusid"`
	DeviceNo     string `json:"device_no"`
}
type Record struct {
	Version         int             `json:"version"`
	RequestKey      string          `json:"request_key"`
	Backend         string          `json:"backend"`
	Endpoint        string          `json:"endpoint"`
	Binding         Binding         `json:"binding"`
	State           string          `json:"state"`
	SelectionDigest string          `json:"selection_digest"`
	UpdatedAtUnixMS int64           `json:"updated_at_unix_ms"`
	OrderID         string          `json:"order_id,omitempty"`
	OrderJobKey     string          `json:"order_job_key,omitempty"`
	CoreOrderKey    string          `json:"core_order_key,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	Selection       json.RawMessage `json:"selection,omitempty"`
	StartedAtUnixMS *int64          `json:"started_at_unix_ms,omitempty"`
}
type Journal struct {
	Version int               `json:"version"`
	Cusid   string            `json:"cusid"`
	Records map[string]Record `json:"records"`
}

func canonicalFields(data []byte, names ...string) error {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("noncanonical or unknown schema field %q", name)
		}
	}
	return nil
}

func (b *Binding) UnmarshalJSON(data []byte) error {
	if err := canonicalFields(data, "account_alias", "cusid", "device_no"); err != nil {
		return err
	}
	type wireBinding Binding
	var out wireBinding
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*b = Binding(out)
	return nil
}

func (j *Journal) UnmarshalJSON(data []byte) error {
	if err := canonicalFields(data, "version", "cusid", "records"); err != nil {
		return err
	}
	type wireJournal Journal
	var out wireJournal
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*j = Journal(out)
	return nil
}

func (r *Record) UnmarshalJSON(data []byte) error {
	if err := canonicalFields(data, "version", "request_key", "backend", "endpoint", "binding", "state", "selection_digest", "updated_at_unix_ms", "order_id", "order_job_key", "core_order_key", "reason", "selection", "started_at_unix_ms"); err != nil {
		return err
	}
	type wireRecord Record
	var out wireRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("record fields cannot be null")
		}
	}
	*r = Record(out)
	return nil
}

type Options struct{ StateRoot, Cusid, AccountAlias, DeviceNo, RequestKey string }

func BaseName(cusid string) string {
	h := sha256.Sum256([]byte(cusid))
	return "account-" + hex.EncodeToString(h[:])
}

func validateOptions(o Options) error {
	if !filepath.IsAbs(o.StateRoot) || strings.TrimSpace(o.Cusid) == "" || strings.TrimSpace(o.AccountAlias) == "" || strings.TrimSpace(o.DeviceNo) == "" || !requestKeyPattern.MatchString(o.RequestKey) {
		return errors.New("absolute state-root and cusid/account-alias/device-no/request-key are required")
	}
	st, err := os.Lstat(o.StateRoot)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return errors.New("state-root must be an existing 0700 directory")
	}
	parent, err := os.Lstat(filepath.Dir(o.StateRoot))
	if err != nil {
		return err
	}
	if !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
		return errors.New("state-root parent must be a durably provisioned real directory, not writable by other users")
	}
	return nil
}

// Kept as a function value for deterministic fsync-failure tests. Callers do
// not override it. Tests using the hook must not run in parallel.
var syncDirectory = func(path string) error {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return f.Sync()
}

func syncStateDirectory(root string) error {
	// The direct parent is an operator-provisioned durable anchor. Repeat on
	// every use so an earlier failed final-directory initialization is safe.
	if err := syncDirectory(root); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(root))
}

func lock(ctx context.Context, o Options) (*os.File, error) {
	if err := validateOptions(o); err != nil {
		return nil, err
	}
	if err := syncStateDirectory(o.StateRoot); err != nil {
		return nil, err
	}
	p := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".lock")
	fd, err := syscall.Open(p, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), p)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		f.Close()
		return nil, errors.New("lock must be a regular 0600 file")
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }

func read(o Options) (*Journal, error) {
	p := filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json")
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, syscall.ENOENT) {
		return &Journal{Version: 1, Cusid: o.Cusid, Records: map[string]Record{}}, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return nil, errors.New("journal must be a regular 0600 file")
	}
	if st.Size() > 8_388_608 {
		return nil, errors.New("journal exceeds size limit")
	}
	if err := uniqueJSONKeys(json.NewDecoder(f)); err != nil {
		return nil, fmt.Errorf("invalid journal: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var j Journal
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&j); err != nil {
		return nil, fmt.Errorf("invalid journal: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err == nil {
		return nil, errors.New("trailing journal data")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	if j.Version != 1 || j.Cusid != o.Cusid || j.Records == nil {
		return nil, errors.New("unknown journal schema or account")
	}
	for k, r := range j.Records {
		if err := validateRecord(k, r, o.Cusid); err != nil {
			return nil, err
		}
		if r.State == "started" || r.State == "delivery_unknown" || r.State == "queued" {
			return nil, errors.New("account has unresolved delivery")
		}
	}
	return &j, nil
}

// Duplicate JSON keys can hide an unresolved claim from another decoder.
func uniqueJSONKeys(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[s] = true
			if err := uniqueJSONKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSONKeys(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = d.Token()
	return err
}
func validateRecord(k string, r Record, cusid string) error {
	digest, err := hex.DecodeString(r.SelectionDigest)
	if !requestKeyPattern.MatchString(k) || r.RequestKey != k || r.Version != 1 || (r.Backend != "app" && r.Backend != "legacy") || r.Endpoint != "/nj/order/order" || r.Binding.Cusid != cusid || r.Binding.AccountAlias == "" || r.Binding.DeviceNo == "" || r.UpdatedAtUnixMS <= 0 || err != nil || len(digest) != 32 || r.SelectionDigest != strings.ToLower(r.SelectionDigest) {
		return errors.New("invalid record schema or binding")
	}
	if len(r.Selection) != 0 && !bytes.HasPrefix(bytes.TrimSpace(r.Selection), []byte("{")) {
		return errors.New("selection must be an object")
	}
	if r.StartedAtUnixMS == nil || *r.StartedAtUnixMS <= 0 || r.UpdatedAtUnixMS < *r.StartedAtUnixMS {
		return errors.New("start time is required and update cannot precede start")
	}
	if utf8.RuneCountInString(r.Reason) > 256 || utf8.RuneCountInString(r.Binding.AccountAlias) > 256 || utf8.RuneCountInString(r.Binding.Cusid) > 256 || utf8.RuneCountInString(r.Binding.DeviceNo) > 256 {
		return errors.New("record binding or reason exceeds length limit")
	}
	if r.Backend == "app" {
		digest, err := appSelectionDigest(r.Selection)
		if err != nil || digest != r.SelectionDigest {
			return errors.New("invalid app selection or selection digest mismatch")
		}
	}
	for _, value := range []string{r.OrderID, r.OrderJobKey, r.CoreOrderKey} {
		if value != "" && !identifierPattern.MatchString(value) {
			return errors.New("invalid order identifier")
		}
	}
	if r.State == "created" {
		switch strings.ToLower(r.OrderID) {
		case "", "0", "null", "none", "pending":
			return errors.New("created record requires verified order ID")
		}
	}
	switch r.State {
	case "started", "delivery_unknown", "queued", "created":
	default:
		return errors.New("unknown record state")
	}
	return nil
}

func appSelectionDigest(raw json.RawMessage) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	if len(fields) != 10 {
		return "", errors.New("selection requires exactly ten fields")
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", errors.New("selection fields cannot be null")
		}
	}
	var selection client.BridgeSelection
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&selection); err != nil {
		return "", err
	}
	if err := selection.Validate(); err != nil {
		return "", err
	}
	// Validated identifiers, money, delivery, and field names are ASCII. A
	// sorted map of normalized typed values therefore matches Python's
	// json.dumps(sort_keys=True, separators=(',', ':'), ensure_ascii=True).
	normalized, err := json.Marshal(selection)
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(normalized, &fields); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
func write(o Options, j *Journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(o.StateRoot, ".orderguard-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(o.StateRoot, BaseName(o.Cusid)+".json")); err != nil {
		return err
	}
	d, err := os.Open(o.StateRoot)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Precheck is advisory only. Run always repeats validation while holding the lock.
func Precheck(ctx context.Context, o Options) error {
	f, err := lock(ctx, o)
	if err != nil {
		return err
	}
	defer unlock(f)
	_, err = read(o)
	return err
}

// Run claims the account durably before starting a child and keeps its lock until
// the child has exited and delivery_unknown has been durably recorded.
func Run(ctx context.Context, o Options, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return errors.New("legacy binary is required")
	}
	f, err := lock(ctx, o)
	if err != nil {
		return err
	}
	defer unlock(f)
	j, err := read(o)
	if err != nil {
		return err
	}
	if _, ok := j.Records[o.RequestKey]; ok {
		return errors.New("request-key already exists")
	}
	selection, _ := json.Marshal(args)
	digest := sha256.Sum256(selection)
	started := time.Now().UnixMilli()
	r := Record{Version: 1, RequestKey: o.RequestKey, Backend: "legacy", Endpoint: "/nj/order/order", Binding: Binding{AccountAlias: o.AccountAlias, Cusid: o.Cusid, DeviceNo: o.DeviceNo}, State: "started", SelectionDigest: hex.EncodeToString(digest[:]), UpdatedAtUnixMS: started, StartedAtUnixMS: &started}
	j.Records[o.RequestKey] = r
	if err = write(o, j); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// A legacy launcher can spawn workers. Keep them in a separate group so
	// cancellation and normal launcher exit both terminate remaining workers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second
	childErr := cmd.Run()
	cleanupErr := killProcessGroup(cmd)
	r.State = "delivery_unknown"
	r.Reason = "legacy_process_finished_without_verified_business_result"
	r.UpdatedAtUnixMS = time.Now().UnixMilli()
	if r.UpdatedAtUnixMS < started {
		r.UpdatedAtUnixMS = started
	}
	j.Records[o.RequestKey] = r
	if err = write(o, j); err != nil {
		return fmt.Errorf("delivery_unknown journal update failed; account remains unresolved: %w", err)
	}
	if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrProcessDone) {
		return fmt.Errorf("%w: legacy process group cleanup failed", ErrDeliveryUnknown)
	}
	if childErr != nil {
		return fmt.Errorf("%w: child did not complete successfully", ErrDeliveryUnknown)
	}
	return ErrDeliveryUnknown
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
