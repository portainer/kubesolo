package system

import (
	"strings"
	"testing"
)

func TestParseMemTotal(t *testing.T) {
	meminfo := "MemTotal:        4005020 kB\nMemFree:          212340 kB\n"
	if got, want := parseMemTotal(strings.NewReader(meminfo)), uint64(4005020*1024); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
	if got := parseMemTotal(strings.NewReader("garbage\n")); got != 0 {
		t.Errorf("unparseable meminfo should report 0 (unknown), got %d", got)
	}
}
