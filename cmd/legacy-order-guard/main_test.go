package main

import "testing"

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--timeout=0"}, {"precheck", "--timeout=-1s"}, {"--unknown"}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
