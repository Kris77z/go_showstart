package config

import "testing"

func TestBridgeSelectionRequiresLoopbackAndBinding(t *testing.T) {
	base := Bridge{Endpoint: "http://127.0.0.1:8765", AccountAlias: "a", Cusid: "123", DeviceNo: "dev"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://example.org:8765", "http://0.0.0.0:8765", "http://127.0.0.1:8765/path", "http://u:p@127.0.0.1:8765", "http://127.0.0.1", "https://127.0.0.1:8765", "http://127.0.0.1:8765?token=x"} {
		b := base
		b.Endpoint = endpoint
		if b.Validate() == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	base.DeviceNo = ""
	if base.Validate() == nil {
		t.Fatal("missing device accepted")
	}
}

func TestMonitorDefaultsRemainReadOnly(t *testing.T) {
	cfg := &Config{Showstart: &Showstart{}, Monitor: &Monitor{Enable: true, Keywords: []string{"fixture"}, WebhookURL: "http://127.0.0.1:1"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Showstart.BaseURL != "" || cfg.Monitor.AutoPurchaseEnable {
		t.Fatal("monitor default changed")
	}
	cfg.Monitor.AutoPurchaseEnable = true
	if cfg.Validate() == nil {
		t.Fatal("legacy automatic submit accepted")
	}
}
